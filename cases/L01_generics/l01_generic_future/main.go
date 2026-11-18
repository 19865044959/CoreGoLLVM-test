// L01: 泛型 Future/Promise：一次性完成、广播等待、Map/All 组合、错误传播与取消观察。
// 对应 pion/webrtc GatheringCompletePromise 与 tailscale singleflight 的共享结果语义。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type futPayload struct {
	id   int64
	name string
	blob []byte
}

type futResult[T any] struct {
	val T
	err error
}

type future[T any] struct {
	done chan struct{}
	once sync.Once
	res  futResult[T]
}

type promise[T any] struct {
	f *future[T]
}

func newPromise[T any]() (*promise[T], *future[T]) {
	f := &future[T]{done: make(chan struct{})}
	return &promise[T]{f: f}, f
}

// resolve/reject 只有第一次生效；res 在 close(done) 前写入，等待者经 close 同步可见。
func (p *promise[T]) resolve(v T) {
	p.f.once.Do(func() {
		p.f.res = futResult[T]{val: v}
		close(p.f.done)
	})
}

func (p *promise[T]) reject(err error) {
	p.f.once.Do(func() {
		p.f.res = futResult[T]{err: err}
		close(p.f.done)
	})
}

func (f *future[T]) await() (T, error) {
	<-f.done
	return f.res.val, f.res.err
}

// awaitOr 在 cancel 关闭时放弃等待；f 完成时返回 ok=true。
func (f *future[T]) awaitOr(cancel <-chan struct{}) (T, error, bool) {
	select {
	case <-f.done:
		return f.res.val, f.res.err, true
	case <-cancel:
		var zero T
		return zero, nil, false
	}
}

func mapFuture[A, B any](f *future[A], fn func(A) (B, error)) *future[B] {
	p, out := newPromise[B]()
	go func() {
		v, err := f.await()
		if err != nil {
			p.reject(err)
			return
		}
		b, err := fn(v)
		if err != nil {
			p.reject(err)
			return
		}
		p.resolve(b)
	}()
	return out
}

func allFutures[T any](fs []*future[T]) *future[[]T] {
	p, out := newPromise[[]T]()
	go func() {
		vals := make([]T, len(fs))
		for i, f := range fs {
			v, err := f.await()
			if err != nil {
				p.reject(err)
				return
			}
			vals[i] = v
		}
		p.resolve(vals)
	}()
	return out
}

var (
	failed     int32
	gcSink     [][]byte
	uafSeed    int64 = 1
	errFailure       = errors.New("future rejected")
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	n, err := strconv.ParseInt(os.Getenv("UAF_SEED"), 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func runBasics() {
	// 先 resolve 后 GC 再 await：值必须由 future 保活。
	p, f := newPromise[*futPayload]()
	payload := &futPayload{id: 42, name: "answer", blob: []byte{1, 2, 3, 4}}
	p.resolve(payload)
	gcStress()
	v, err := f.await()
	ck(err == nil && v == payload, "resolved pointer identity lost")
	ck(v != nil && v.name == "answer" && v.blob[2] == 3, "resolved payload corrupted after GC")

	// 先 GC 再 resolve：闭包捕获的指针也要保活。
	p2, f2 := newPromise[*futPayload]()
	payload2 := &futPayload{id: 43, name: "late", blob: []byte{9, 8, 7}}
	gcStress()
	p2.resolve(payload2)
	gcStress()
	v2, err := f2.await()
	ck(err == nil && v2 == payload2 && v2.blob[0] == 9, "late resolve pointer corrupted")

	// 首次完成生效，后续 resolve/reject 都被忽略。
	p3, f3 := newPromise[int64]()
	p3.resolve(111)
	p3.resolve(222)
	p3.reject(errFailure)
	v3, err := f3.await()
	ck(err == nil && v3 == 111, fmt.Sprintf("first completion not retained got=%d err=%v", v3, err))

	// 拒绝路径的错误指针保持身份。
	p4, f4 := newPromise[string]()
	p4.reject(errFailure)
	_, err = f4.await()
	ck(err == errFailure, "rejection error identity lost")

	// 取消通道未完成：ok=false；完成后用 nil 取消通道等待成功。
	p5, f5 := newPromise[int64]()
	cancel := make(chan struct{})
	close(cancel)
	_, _, ok := f5.awaitOr(cancel)
	ck(!ok, "awaitOr should report cancellation before completion")
	p5.resolve(55)
	var never chan struct{}
	v5, err5, ok5 := f5.awaitOr(never)
	ck(ok5 && err5 == nil && v5 == 55, "awaitOr failed after completion")

	// 多个等待者共享同一结果。
	p6, f6 := newPromise[*futPayload]()
	payload6 := &futPayload{id: 7, name: "broadcast", blob: []byte{6}}
	var wg sync.WaitGroup
	var got [4]*futPayload
	for i := 0; i < len(got); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			x, err := f6.await()
			if err == nil {
				got[i] = x
			}
		}(i)
	}
	p6.resolve(payload6)
	wg.Wait()
	for i := range got {
		ck(got[i] == payload6, fmt.Sprintf("broadcast awaiter %d mismatch", i))
	}
}

func runComposition() {
	// Map 链：int64 -> string -> int。
	p, f := newPromise[int64]()
	m1 := mapFuture(f, func(v int64) (string, error) {
		return fmt.Sprintf("n=%d", v), nil
	})
	m2 := mapFuture(m1, func(s string) (int, error) {
		return len(s), nil
	})
	p.resolve(21)
	got, err := m2.await()
	ck(err == nil && got == 4, fmt.Sprintf("map chain got=%d err=%v", got, err))

	// 错误沿链传播，下游 fn 不得执行。
	pErr, fErr := newPromise[int64]()
	var calls int32
	mErr := mapFuture(fErr, func(v int64) (string, error) {
		atomic.AddInt32(&calls, 1)
		return "should-not-run", nil
	})
	pErr.reject(errFailure)
	_, err = mErr.await()
	ck(err == errFailure, "map did not propagate rejection")
	ck(atomic.LoadInt32(&calls) == 0, "map fn executed after rejection")

	// Map fn 自身返回错误。
	pBad, fBad := newPromise[int64]()
	badErr := errors.New("map failure")
	mBad := mapFuture(fBad, func(int64) (int64, error) { return 0, badErr })
	pBad.resolve(1)
	if _, err := mBad.await(); err != badErr {
		ck(false, "map returned wrong error")
	}
}

func runAll(seed int64) {
	n := 24 + int(seed%16)
	ps := make([]*promise[*futPayload], n)
	fs := make([]*future[*futPayload], n)
	want := make([]*futPayload, n)
	for i := 0; i < n; i++ {
		ps[i], fs[i] = newPromise[*futPayload]()
		want[i] = &futPayload{
			id:   int64(i),
			name: fmt.Sprintf("task-%02d", i),
			blob: []byte{byte(i), byte(i * 3)},
		}
	}
	gcStress()

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ps[i].resolve(want[i])
		}(i)
	}
	close(start)
	all := allFutures(fs)
	vals, err := all.await()
	ck(err == nil, fmt.Sprintf("all failed: %v", err))
	ck(len(vals) == n, fmt.Sprintf("all length got=%d want=%d", len(vals), n))
	for i := 0; i < len(vals) && i < n; i++ {
		ck(vals[i] == want[i], fmt.Sprintf("all order/pointer mismatch at %d", i))
		if vals[i] != nil {
			ck(vals[i].id == int64(i) && vals[i].blob[0] == byte(i), "all payload corrupted")
		}
	}
	wg.Wait()

	// All 中的错误按参数序传播；用独立批避免与上面的结果混淆。
	n2 := 8
	ps2 := make([]*promise[int64], n2)
	fs2 := make([]*future[int64], n2)
	for i := 0; i < n2; i++ {
		ps2[i], fs2[i] = newPromise[int64]()
	}
	all2 := allFutures(fs2)
	for i := 0; i < n2; i++ {
		if i == 3 {
			ps2[i].reject(errFailure)
		} else {
			ps2[i].resolve(int64(i))
		}
	}
	if _, err := all2.await(); err != errFailure {
		ck(false, "all did not propagate rejection")
	}

	// 空集合 All 立即完成。
	empty, _ := newPromise[int64]()
	_ = empty
	allEmpty := allFutures([]*future[int64]{})
	valsEmpty, err := allEmpty.await()
	ck(err == nil && len(valsEmpty) == 0, "empty all failed")
}

func main() {
	uafSeed = envSeed()
	runBasics()
	runComposition()
	runAll(uafSeed)
	finish("l01_generic_future")
}
