// L06: 嵌入接口与菱形方法集：stdlib io 接口组合、重复方法去重、Implements/AssignableTo
// 方向、nil 嵌入接口调用 panic 与包装链跨 GC 动态派发。
package main

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type ReadCloser interface {
	io.Reader
	io.Closer
}

type ReadWriter interface {
	io.Reader
	io.Writer
}

// ReadWriteCloser 通过 ReadCloser 与 ReadWriter 菱形嵌入 io.Reader，方法集必须去重。
type ReadWriteCloser interface {
	ReadCloser
	ReadWriter
}

type SeekReadWriteCloser interface {
	ReadWriteCloser
	io.Seeker
}

type buffer struct {
	data   []byte
	tag    *node
	reads  int64
	writes int64
	seeks  int64
	closed bool
}

func (b *buffer) Read(p []byte) (int, error) {
	b.reads++
	return copy(p, b.data), nil
}

func (b *buffer) Write(p []byte) (int, error) {
	b.writes++
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *buffer) Close() error {
	b.closed = true
	return nil
}

func (b *buffer) Seek(offset int64, whence int) (int64, error) {
	b.seeks++
	return offset, nil
}

// wrapper 在结构体中嵌入接口，方法集由嵌入接口提升。
type wrapper struct {
	ReadWriteCloser
	tag *node
}

var (
	_ ReadWriteCloser     = (*buffer)(nil)
	_ SeekReadWriteCloser = (*buffer)(nil)
	_ io.Reader           = (*buffer)(nil)
	_ ReadWriteCloser     = wrapper{}
)

var (
	failed    int32
	gcSink    [][]byte
	ifaceSink []any
	uafSeed   int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func mustPanic(name string, fn func()) {
	defer func() {
		if recover() == nil {
			ck(false, name+" did not panic")
		}
	}()
	fn()
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
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
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()
	const rounds = 20

	dt := reflect.TypeOf((*SeekReadWriteCloser)(nil)).Elem()
	rwcT := reflect.TypeOf((*ReadWriteCloser)(nil)).Elem()
	rcT := reflect.TypeOf((*ReadCloser)(nil)).Elem()
	rwT := reflect.TypeOf((*ReadWriter)(nil)).Elem()
	errT := reflect.TypeOf((*error)(nil)).Elem()
	byteT := reflect.TypeOf([]byte(nil))

	// 菱形嵌入：Read 只计一次。
	ck(dt.NumMethod() == 4, "seek read write closer has 4 methods")
	ck(rwcT.NumMethod() == 3, "read write closer has 3 methods")
	ck(rcT.NumMethod() == 2, "read closer has 2 methods")
	ck(rwT.NumMethod() == 2, "read writer has 2 methods")
	wantNames := []string{"Close", "Read", "Seek", "Write"}
	for i, name := range wantNames {
		ck(dt.Method(i).Name == name, "interface method order "+strconv.Itoa(i))
		ck(dt.Method(i).Index == i, "interface method index "+strconv.Itoa(i))
		ck(!dt.Method(i).Func.IsValid(), "interface Method.Func is zero")
	}
	ck(dt.Method(1).Type.NumIn() == 1 && dt.Method(1).Type.In(0) == byteT, "Read signature input")
	ck(dt.Method(1).Type.NumOut() == 2 && dt.Method(1).Type.Out(0) == reflect.TypeOf(0) && dt.Method(1).Type.Out(1) == errT, "Read signature outputs")
	ck(dt.Method(2).Type.NumIn() == 2 && dt.Method(2).Type.In(0) == reflect.TypeOf(int64(0)) && dt.Method(2).Type.In(1) == reflect.TypeOf(0), "Seek signature inputs")
	ck(dt.Method(2).Type.NumOut() == 2 && dt.Method(2).Type.Out(0) == reflect.TypeOf(int64(0)) && dt.Method(2).Type.Out(1) == errT, "Seek signature outputs")

	bufT := reflect.TypeOf(&buffer{})
	valT := reflect.TypeOf(buffer{})
	ck(bufT.Implements(dt) && bufT.Implements(rwcT), "pointer buffer implements composed interfaces")
	ck(!valT.Implements(dt), "value buffer lacks pointer receiver methods")
	ck(valT.NumMethod() == 0 && bufT.NumMethod() == 4, "buffer method counts")
	ck(dt.Implements(rwcT), "wider interface implements narrower")
	ck(!rwcT.Implements(dt), "narrower interface does not implement wider")
	ck(dt.AssignableTo(rwcT), "wider interface assignable to narrower")
	ck(!rwcT.AssignableTo(dt), "narrower interface not assignable to wider")
	ck(dt.AssignableTo(rcT) && rwcT.AssignableTo(rcT), "assignable to read closer")
	ck(reflect.TypeOf(wrapper{}).Implements(rwcT), "wrapper value implements embedded interface")
	ck(!reflect.TypeOf(&wrapper{}).Implements(dt), "wrapper does not gain seeker through embedding")

	anonRC := reflect.TypeOf((*interface {
		io.Reader
		io.Closer
	})(nil)).Elem()
	ck(anonRC != rcT, "named and unnamed interface types differ")
	ck(anonRC.AssignableTo(rcT) && rcT.AssignableTo(anonRC), "structurally identical interfaces assignable both ways")
	u1 := reflect.TypeOf((*interface{ Read([]byte) (int, error) })(nil)).Elem()
	u2 := reflect.TypeOf((*interface{ Read([]byte) (int, error) })(nil)).Elem()
	ck(u1 == u2, "identical unnamed interfaces equal")

	var nilWrap wrapper
	mustPanic("call through nil embedded interface", func() {
		nilWrap.Read(make([]byte, 4))
	})

	for r := 0; r < rounds; r++ {
		wantData := fmt.Sprintf("seed-%d", r)
		b := &buffer{data: []byte(wantData), tag: &node{V: int64(r), Label: "buf"}}
		var rc ReadCloser = b
		var rw ReadWriter = b
		var rwc ReadWriteCloser = b
		var srwc SeekReadWriteCloser = b
		var w ReadWriteCloser = wrapper{ReadWriteCloser: b, tag: &node{V: int64(r + 1), Label: "wrap"}}
		ifaceSink = []any{rc, rw, rwc, srwc, w, b}
		gcStress()

		buf := make([]byte, 64)
		n, err := rc.Read(buf)
		ck(err == nil && n == len(wantData) && string(buf[:n]) == wantData, "read through embedded interface after GC")
		nw, err := rw.Write([]byte("+x"))
		ck(err == nil && nw == 2 && b.writes == 1, "write through read writer after GC")
		ck(b.tag != nil && b.tag.V == int64(r) && b.tag.Label == "buf", "buffer tag survives GC")
		st, err := srwc.Seek(42, io.SeekStart)
		ck(err == nil && st == 42 && b.seeks == 1, "seek through composed interface")
		n, err = w.Read(buf)
		ck(err == nil && n == len(wantData)+2, "read through wrapper chain after GC")
		wrapped := w.(wrapper)
		ck(wrapped.tag != nil && wrapped.tag.V == int64(r+1), "wrapper payload survives GC")

		_, ok := ifaceSink[0].(io.Seeker)
		ck(ok, "dynamic buffer implements seeker")
		_, ok = ifaceSink[0].(io.ReaderAt)
		ck(!ok, "dynamic buffer does not implement ReaderAt")
		narrow, ok := ifaceSink[3].(ReadCloser)
		ck(ok && narrow == rc, "wider interface value asserts to read closer")
		seeker, ok := ifaceSink[3].(io.Seeker)
		ck(ok && seeker == srwc, "seeker assertion keeps interface identity")
		ck(reflect.TypeOf(ifaceSink[0]).Implements(dt), "dynamic type implements composed interface")
		ck(reflect.TypeOf(ifaceSink[4]).Implements(rwcT), "wrapper dynamic type implements interface")

		if err := rc.Close(); err != nil || !b.closed {
			ck(false, "close through read closer")
		}
		gcStress()
		ck(b.closed && b.tag.V == int64(r), "closed buffer state after GC")
	}
	finish("l06_iface_embed_diamond")
}
