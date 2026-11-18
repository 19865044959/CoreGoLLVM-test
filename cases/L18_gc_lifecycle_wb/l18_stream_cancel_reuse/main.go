// L18: Prepare/Write/Flush/Cancel 状态机；取消丢弃的数据不得泄漏，取消后对象可再次 Prepare 复用。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

var (
	failed             int32
	errNotPrepared     = errors.New("stream: not prepared")
	errAlreadyPrepared = errors.New("stream: already prepared")
	errCanceled        = errors.New("stream: canceled")
)

const (
	stateIdle = iota
	statePrepared
	stateCanceled
)

type kv struct {
	key []byte
	val []byte
}

type sink struct {
	entries []kv
}

func (s *sink) add(k, v []byte) {
	s.entries = append(s.entries, kv{key: append([]byte(nil), k...), val: append([]byte(nil), v...)})
}

type streamWriter struct {
	state   int
	pending []kv
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 12; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func (w *streamWriter) Prepare() error {
	if w.state == statePrepared {
		return errAlreadyPrepared
	}
	w.pending = w.pending[:0]
	w.state = statePrepared
	return nil
}

func (w *streamWriter) Write(key, val []byte) error {
	if w.state == stateCanceled {
		return errCanceled
	}
	if w.state != statePrepared {
		return errNotPrepared
	}
	// 写入时拷贝，调用方随后可修改/释放输入。
	w.pending = append(w.pending, kv{
		key: append([]byte(nil), key...),
		val: append([]byte(nil), val...),
	})
	return nil
}

func (w *streamWriter) Flush(s *sink) error {
	if w.state == stateCanceled {
		return errCanceled
	}
	if w.state != statePrepared {
		return errNotPrepared
	}
	for _, e := range w.pending {
		s.add(e.key, e.val)
	}
	for i := range w.pending {
		w.pending[i] = kv{}
	}
	w.pending = w.pending[:0]
	w.state = stateIdle
	return nil
}

func (w *streamWriter) Cancel() error {
	if w.state == stateCanceled {
		return errCanceled
	}
	if w.state != statePrepared {
		return errNotPrepared
	}
	for i := range w.pending {
		w.pending[i] = kv{}
	}
	w.pending = w.pending[:0]
	w.state = stateCanceled
	return nil
}

func call(f func() error) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	err = f()
	return
}

func expectErr(what string, f func() error, want error) {
	err, pan := call(f)
	ck(!pan, what+": unexpected panic")
	ck(!pan && err == want, what+": wrong result")
}

func valBytes(seed int, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((seed*17 + i*7) % 251)
	}
	return b
}

func main() {
	sk := &sink{}
	w := &streamWriter{}

	// 未 Prepare：全部操作可判读。
	expectErr("write-idle", func() error { return w.Write([]byte("k"), []byte("v")) }, errNotPrepared)
	expectErr("flush-idle", func() error { return w.Flush(sk) }, errNotPrepared)
	expectErr("cancel-idle", func() error { return w.Cancel() }, errNotPrepared)

	// 正常批次：Write 拷贝语义 + GC。
	expectErr("prepare-1", w.Prepare, nil)
	expectErr("prepare-twice", w.Prepare, errAlreadyPrepared)
	k1 := []byte("key-000")
	v1 := valBytes(1, 96)
	expectErr("write-1", func() error { return w.Write(k1, v1) }, nil)
	for i := range k1 {
		k1[i] = 0xEE
	}
	for i := range v1 {
		v1[i] = 0xEE
	}
	gcStress()
	expectErr("flush-1", func() error { return w.Flush(sk) }, nil)
	if len(sk.entries) != 1 {
		ck(false, "flush-1 entry count")
	} else {
		e := sk.entries[0]
		ck(bytes.Equal(e.key, []byte("key-000")), "write did not copy key")
		ck(bytes.Equal(e.val, valBytes(1, 96)), "write did not copy value")
	}
	expectErr("write-after-flush", func() error { return w.Write([]byte("x"), []byte("y")) }, errNotPrepared)

	// 取消：丢弃 pending；取消后的操作必须返回 errCanceled，且对象必须可再次 Prepare。
	expectErr("prepare-2", w.Prepare, nil)
	cancelKey := []byte("cancel-key")
	cancelVal := valBytes(2, 128)
	expectErr("write-cancel", func() error { return w.Write(cancelKey, cancelVal) }, nil)
	gcStress()
	expectErr("cancel-1", w.Cancel, nil)
	expectErr("cancel-again", w.Cancel, errCanceled)
	expectErr("write-after-cancel", func() error { return w.Write([]byte("z"), []byte("z")) }, errCanceled)
	expectErr("flush-after-cancel", func() error { return w.Flush(sk) }, errCanceled)
	gcStress()
	// 取消批次的数据不得出现在 sink。
	for _, e := range sk.entries {
		ck(!bytes.Equal(e.key, cancelKey), "canceled entry leaked into sink")
	}
	expectErr("prepare-after-cancel", w.Prepare, nil)
	k2 := []byte("key-001")
	v2 := valBytes(3, 65)
	expectErr("write-2", func() error { return w.Write(k2, v2) }, nil)
	expectErr("flush-2", func() error { return w.Flush(sk) }, nil)
	ck(len(sk.entries) == 2, "reuse after cancel: entry count")
	if len(sk.entries) == 2 {
		ck(bytes.Equal(sk.entries[1].key, k2) && bytes.Equal(sk.entries[1].val, v2), "reuse after cancel: data")
	}

	// 多轮复用：Flush/Cancel 交替，期间多次 GC。
	var want []kv
	want = append(want, kv{key: []byte("key-000"), val: valBytes(1, 96)})
	want = append(want, kv{key: []byte("key-001"), val: valBytes(3, 65)})
	for r := 0; r < 30; r++ {
		expectErr("cycle-prepare", w.Prepare, nil)
		var batch []kv
		n := 2 + r%3
		for j := 0; j < n; j++ {
			k := []byte(fmt.Sprintf("k-%02d-%02d", r, j))
			v := valBytes(r*10+j, 16+j*8)
			expectErr("cycle-write", func() error { return w.Write(k, v) }, nil)
			batch = append(batch, kv{key: append([]byte(nil), k...), val: append([]byte(nil), v...)})
		}
		if r%3 == 2 {
			expectErr("cycle-cancel", w.Cancel, nil)
			gcStress() // 取消批次的内部拷贝已丢弃，sink 不得出现这些 key
		} else {
			expectErr("cycle-flush", func() error { return w.Flush(sk) }, nil)
			want = append(want, batch...)
		}
		if r%5 == 0 {
			gcStress()
		}
	}

	if len(sk.entries) != len(want) {
		ck(false, fmt.Sprintf("final sink size=%d want=%d", len(sk.entries), len(want)))
	}
	for i := 0; i < len(sk.entries) && i < len(want); i++ {
		ck(bytes.Equal(sk.entries[i].key, want[i].key), fmt.Sprintf("sink[%d] key mismatch", i))
		ck(bytes.Equal(sk.entries[i].val, want[i].val), fmt.Sprintf("sink[%d] value mismatch", i))
	}
	gcStress()
	for i := 0; i < len(sk.entries) && i < len(want); i++ {
		ck(bytes.Equal(sk.entries[i].val, want[i].val), fmt.Sprintf("sink[%d] value corrupted after final GC", i))
	}

	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED l18_stream_cancel_reuse (%d failed checks)\n", n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS l18_stream_cancel_reuse")
}
