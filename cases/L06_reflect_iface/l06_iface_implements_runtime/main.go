// L06: 运行时接口满足检查：Implements/AssignableTo/ConvertibleTo 矩阵与动态派发。
package main

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type bufRW struct {
	data []byte
	off  int
	tag  *node
}

func (b *bufRW) Read(p []byte) (int, error) {
	if b == nil {
		return 0, io.EOF
	}
	if b.off >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.off:])
	b.off += n
	return n, nil
}

func (b *bufRW) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *bufRW) Close() error { return nil }

func (b *bufRW) Error() string { return b.tag.Label }

type roOnly struct {
	tag *node
}

func (r *roOnly) Read(p []byte) (int, error) {
	if r == nil || r.tag == nil {
		return 0, io.EOF
	}
	p[0] = byte(r.tag.V)
	return 1, nil
}

type valReader struct {
	tag *node
}

func (v valReader) Read(p []byte) (int, error) {
	p[0] = byte(v.tag.V)
	return 1, nil
}

type errOnly struct {
	tag *node
}

func (e errOnly) Error() string { return e.tag.Label }

var (
	failed  int32
	gcSink  [][]byte
	anySink any
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
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

	tReader := reflect.TypeOf((*io.Reader)(nil)).Elem()
	tWriter := reflect.TypeOf((*io.Writer)(nil)).Elem()
	tCloser := reflect.TypeOf((*io.Closer)(nil)).Elem()
	tRWC := reflect.TypeOf((*io.ReadWriteCloser)(nil)).Elem()
	tAny := reflect.TypeOf((*any)(nil)).Elem()
	tErr := reflect.TypeOf((*error)(nil)).Elem()
	tBufPtr := reflect.TypeOf(&bufRW{})
	tBufVal := reflect.TypeOf(bufRW{})
	tRoPtr := reflect.TypeOf(&roOnly{})
	tVal := reflect.TypeOf(valReader{})
	tValPtr := reflect.TypeOf(&valReader{})
	tErrOnly := reflect.TypeOf(errOnly{})

	ck(tReader.NumMethod() == 1 && tWriter.NumMethod() == 1 && tCloser.NumMethod() == 1, "io single-method interfaces")
	ck(tRWC.NumMethod() == 3, "io.ReadWriteCloser method count")
	ck(tAny.NumMethod() == 0, "empty interface has no methods")
	ck(tErr.NumMethod() == 1 && tErr.Name() == "error", "error interface shape")
	mnames := []string{}
	for i := 0; i < tRWC.NumMethod(); i++ {
		mnames = append(mnames, tRWC.Method(i).Name)
	}
	sort.Strings(mnames)
	ck(reflect.DeepEqual(mnames, []string{"Close", "Read", "Write"}), "ReadWriteCloser method names")

	type matrixCase struct {
		name string
		typ  reflect.Type
		dst  reflect.Type
		want bool
	}
	cases := []matrixCase{
		{"bufRW ptr->Reader", tBufPtr, tReader, true},
		{"bufRW ptr->Writer", tBufPtr, tWriter, true},
		{"bufRW ptr->Closer", tBufPtr, tCloser, true},
		{"bufRW ptr->ReadWriteCloser", tBufPtr, tRWC, true},
		{"bufRW ptr->error", tBufPtr, tErr, true},
		{"bufRW val->Reader", tBufVal, tReader, false},
		{"bufRW val->ReadWriteCloser", tBufVal, tRWC, false},
		{"roOnly ptr->Reader", tRoPtr, tReader, true},
		{"roOnly ptr->Writer", tRoPtr, tWriter, false},
		{"roOnly ptr->ReadWriteCloser", tRoPtr, tRWC, false},
		{"valReader val->Reader", tVal, tReader, true},
		{"valReader ptr->Reader", tValPtr, tReader, true},
		{"valReader val->Writer", tVal, tWriter, false},
		{"errOnly ->error", tErrOnly, tErr, true},
		{"errOnly ->Reader", tErrOnly, tReader, false},
		{"ReadWriteCloser->Reader", tRWC, tReader, true},
		{"ReadWriteCloser->Closer", tRWC, tCloser, true},
		{"Reader->ReadWriteCloser", tReader, tRWC, false},
		{"Reader->any", tReader, tAny, true},
		{"any->Reader", tAny, tReader, false},
		{"error->any", tErr, tAny, true},
		{"error->Reader", tErr, tReader, false},
		{"ptr->any", tBufPtr, tAny, true},
	}
	perm := rand.New(rand.NewSource(uafSeed)).Perm(len(cases))

	const rounds = 16
	for r := 0; r < rounds; r++ {
		b := &bufRW{data: []byte("abcdefgh"), tag: &node{V: 70 + int64(r), Label: "rw"}}
		var rwc io.ReadWriteCloser = b
		var rd io.Reader = rwc
		var wr io.Writer = rwc
		var cl io.Closer = rwc
		var eb error = b
		var anyv any = rwc
		anySink = []any{rwc, rd, wr, cl, eb, anyv}
		gcStress()

		for _, ci := range perm {
			c := cases[ci]
			imp := c.typ.Implements(c.dst)
			asg := c.typ.AssignableTo(c.dst)
			cv := c.typ.ConvertibleTo(c.dst)
			ck(imp == c.want, "Implements: "+c.name)
			ck(asg == c.want, "AssignableTo: "+c.name)
			ck(cv == c.want, "ConvertibleTo: "+c.name)
		}

		ck(reflect.TypeOf(anyv) == tBufPtr, "dynamic type of interface value")
		ck(reflect.TypeOf(&rwc).Elem() == tRWC, "static interface type via pointer elem")
		ck(reflect.TypeOf(&rd).Elem() == tReader, "io.Reader interface type")
		ck(reflect.ValueOf(&rwc).Elem().MethodByName("Write").IsValid(), "interface method set visible via reflect")
		ck(reflect.ValueOf(&rwc).Elem().Elem().Type() == tBufPtr, "reflect interface Elem gives dynamic value")

		buf := make([]byte, 3)
		n, err := rd.Read(buf)
		ck(n == 3 && string(buf) == "abc" && err == nil, "io.Reader dynamic dispatch")
		n2, err2 := wr.Write([]byte("XY"))
		ck(n2 == 2 && err2 == nil, "io.Writer dynamic dispatch")
		ck(cl.Close() == nil, "io.Closer dynamic dispatch")
		ck(eb.Error() == "rw", "error dynamic dispatch")

		rv := reflect.ValueOf(&rd).Elem()
		out := rv.MethodByName("Read").Call([]reflect.Value{reflect.ValueOf(make([]byte, 2))})
		ck(len(out) == 2 && out[0].Int() == 2 && out[1].IsNil(), "reflect Read on interface value")
		ck(b.off == 5, "bytes consumed across direct and reflect calls")

		_, okReader := anyv.(io.Reader)
		_, okRWC := anyv.(io.ReadWriteCloser)
		_, okStr := anyv.(fmt.Stringer)
		_, okRdRWC := rd.(io.ReadWriteCloser)
		_, okEbReader := eb.(io.Reader)
		ck(okReader && okRWC && !okStr, "type assertion matrix on any")
		ck(okRdRWC, "interface-to-interface assertion uses dynamic type")
		ck(okEbReader, "error value also implements io.Reader")

		var tn io.Reader = (*roOnly)(nil)
		ck(tn != nil, "typed nil interface is not nil")
		tt := reflect.TypeOf(tn)
		ck(tt == tRoPtr && tt.Implements(tReader), "typed nil dynamic type implements Reader")
		_, tnOK := tn.(*roOnly)
		ck(tnOK, "typed nil assertion succeeds")

		func() {
			defer func() { ck(recover() != nil, "failed interface assertion panics") }()
			_ = anyv.(fmt.Stringer)
		}()

		// 通过接口显式转换后再断言。
		conv := io.Reader(rwc)
		conv2 := io.ReadWriteCloser(conv.(io.ReadWriteCloser))
		ck(reflect.TypeOf(conv) == tBufPtr && reflect.TypeOf(conv2) == tBufPtr, "interface conversions keep dynamic type")

		gcStress()
		ck(reflect.TypeOf(anyv).Implements(tRWC), "dynamic type Implements after GC")
		ck(b.tag.V == 70+int64(r) && b.tag.Label == "rw", "interface-held pointer survives GC")
		ck(tBufPtr.Implements(tRWC) && !tBufVal.Implements(tRWC), "type matrix stable after GC")
	}
	finish("l06_iface_implements_runtime")
}
