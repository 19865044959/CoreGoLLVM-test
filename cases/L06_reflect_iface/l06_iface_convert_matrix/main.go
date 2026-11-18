// L06: 接口转换矩阵：接口↔接口、接口↔具体类型的断言/赋值/显式转换，跨 GC 一致。
package main

import (
	"fmt"
	"math/rand"
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

type Reader interface{ Read(int64) int64 }
type Writer interface{ Write(int64) int64 }
type ReadWriter interface {
	Reader
	Writer
}
type Closer interface{ Close() int64 }
type ReadWriteCloser interface {
	ReadWriter
	Closer
}
type Named interface{ Name() string }

type valImpl struct {
	tag *node
}

func (v valImpl) Read(x int64) int64 { return v.tag.V + x }
func (v valImpl) Name() string       { return v.tag.Label }

type ptrImpl struct {
	tag *node
}

func (p *ptrImpl) Read(x int64) int64  { return p.tag.V + x }
func (p *ptrImpl) Write(x int64) int64 { return p.tag.V - x }
func (p *ptrImpl) Close() int64        { return p.tag.V }
func (p *ptrImpl) Name() string        { return p.tag.Label }

type errImpl struct {
	tag *node
}

func (e errImpl) Error() string { return e.tag.Label }

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

func envInt(name string, def, max int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		n = max
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 100, 20000)

	tReader := reflect.TypeOf((*Reader)(nil)).Elem()
	tWriter := reflect.TypeOf((*Writer)(nil)).Elem()
	tRW := reflect.TypeOf((*ReadWriter)(nil)).Elem()
	tCloser := reflect.TypeOf((*Closer)(nil)).Elem()
	tRWC := reflect.TypeOf((*ReadWriteCloser)(nil)).Elem()
	tNamed := reflect.TypeOf((*Named)(nil)).Elem()
	tErr := reflect.TypeOf((*error)(nil)).Elem()
	tAny := reflect.TypeOf((*any)(nil)).Elem()
	tVal := reflect.TypeOf(valImpl{})
	tValPtr := reflect.TypeOf(&valImpl{})
	tPtr := reflect.TypeOf(&ptrImpl{})
	tErrImpl := reflect.TypeOf(errImpl{})

	type convCase struct {
		name string
		src  reflect.Type
		dst  reflect.Type
		want bool
	}
	cases := []convCase{
		{"valImpl value->Reader", tVal, tReader, true},
		{"valImpl value->Named", tVal, tNamed, true},
		{"valImpl value->Writer", tVal, tWriter, false},
		{"valImpl ptr->Reader", tValPtr, tReader, true},
		{"ptrImpl ptr->Reader", tPtr, tReader, true},
		{"ptrImpl ptr->Writer", tPtr, tWriter, true},
		{"ptrImpl ptr->ReadWriter", tPtr, tRW, true},
		{"ptrImpl ptr->Closer", tPtr, tCloser, true},
		{"ptrImpl ptr->ReadWriteCloser", tPtr, tRWC, true},
		{"ptrImpl value->Reader", reflect.TypeOf(ptrImpl{}), tReader, false},
		{"errImpl value->error", tErrImpl, tErr, true},
		{"errImpl value->Reader", tErrImpl, tReader, false},
		{"ReadWriter->Reader", tRW, tReader, true},
		{"ReadWriter->ReadWriteCloser", tRW, tRWC, false},
		{"ReadWriteCloser->ReadWriter", tRWC, tRW, true},
		{"ReadWriteCloser->Closer", tRWC, tCloser, true},
		{"Reader->ReadWriter", tReader, tRW, false},
		{"Reader->any", tReader, tAny, true},
		{"any->Reader", tAny, tReader, false},
		{"error->any", tErr, tAny, true},
		{"error->Named", tErr, tNamed, false},
		{"valImpl value->any", tVal, tAny, true},
		{"ptrImpl ptr->any", tPtr, tAny, true},
	}

	ck(tReader.NumMethod() == 1 && tWriter.NumMethod() == 1 && tCloser.NumMethod() == 1, "interface method counts")
	ck(tRW.NumMethod() == 2 && tRWC.NumMethod() == 3 && tNamed.NumMethod() == 1, "composed interface method counts")
	ck(tAny.NumMethod() == 0 && tErr.NumMethod() == 1, "empty/error interface counts")

	perm := rand.New(rand.NewSource(uafSeed)).Perm(len(cases))
	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*197))
		base := int64(r * 31)
		p := &ptrImpl{tag: &node{V: base + 11, Label: fmt.Sprintf("p%d", rnd.Intn(7))}}
		vv := valImpl{tag: &node{V: base + 22, Label: "v"}}
		ev := errImpl{tag: &node{V: base + 33, Label: "err"}}

		var rwc ReadWriteCloser = p
		var rd Reader = rwc
		var wr Writer = rwc
		var cl Closer = rwc
		var named Named = p
		var eb error = ev
		var anyv any = rwc
		var nilAny any
		anySink = []any{rwc, rd, wr, cl, named, eb, anyv, vv}

		for _, ci := range perm {
			c := cases[ci]
			imp := c.src.Implements(c.dst)
			asg := c.src.AssignableTo(c.dst)
			cv := c.src.ConvertibleTo(c.dst)
			ck(imp == c.want, "Implements: "+c.name)
			ck(asg == c.want, "AssignableTo: "+c.name)
			ck(cv == c.want, "ConvertibleTo: "+c.name)
		}

		ck(rd.Read(5) == base+16 && wr.Write(3) == base+8 && cl.Close() == base+11, "interface methods dispatch")
		ck(named.Name() == p.tag.Label && eb.Error() == "err", "named/error dispatch")

		// interface→interface 断言（动态值仍满足）。
		rw2, ok1 := rd.(ReadWriter)
		rwc2, ok2 := rd.(ReadWriteCloser)
		named2, ok3 := anyv.(Named)
		_, bad1 := anyv.(error)
		_, bad2 := eb.(ReadWriter)
		_, bad3 := nilAny.(Reader)
		ck(ok1 && ok2 && ok3 && !bad1 && !bad2 && !bad3, "comma-ok assertion matrix")
		ck(rw2.Write(1) == base+10 && rwc2.Close() == base+11 && named2.Name() == p.tag.Label, "asserted interfaces callable")

		// 显式接口转换保留动态类型。
		c1 := Reader(rwc)
		c2 := rd.(ReadWriter)
		c3 := any(rwc)
		ck(reflect.TypeOf(c1) == tPtr && reflect.TypeOf(c2) == tPtr && reflect.TypeOf(c3) == tPtr, "conversions keep dynamic type")
		ck(reflect.TypeOf(&rwc).Elem() == tRWC, "declared interface type via reflect")
		ck(reflect.TypeOf(&anyv).Elem() == tAny, "any declared type via reflect")
		ck(reflect.ValueOf(&rwc).Elem().Elem().Type() == tPtr, "reflect dynamic type of interface")
		out := reflect.ValueOf(&rd).Elem().MethodByName("Read").Call([]reflect.Value{reflect.ValueOf(int64(2))})
		ck(out[0].Int() == base+13, "reflect call through interface")

		// 值实现者：valImpl 满足 Reader/Named，不满足 Writer。
		var vr Reader = vv
		_, vOk1 := vr.(Named)
		_, vOk2 := vr.(Writer)
		ck(vOk1 && !vOk2 && vr.Read(1) == base+23, "value implementer assertion matrix")

		// 失败断言必须 panic。
		func() {
			defer func() { ck(recover() != nil, "non-comma assertion panic") }()
			_ = anyv.(error)
		}()

		// typed nil 在接口转换中保持 typed nil。
		var tn Reader = (*ptrImpl)(nil)
		conv := any(tn)
		_, tnOK := conv.(ReadWriter)
		ck(tn != nil && conv != nil && tnOK, "typed nil interface conversion")
		ck(reflect.TypeOf(conv) == tPtr && reflect.ValueOf(conv).IsNil(), "typed nil keeps dynamic type")

		gcStress()
		ck(reflect.TypeOf(anyv) == tPtr && named.Name() == p.tag.Label, "dynamic type and data survive GC")
		ck(tPtr.Implements(tRWC) && tRW.Implements(tReader) && !tAny.Implements(tReader), "Implements matrix stable after GC")
		ck(c1.(ReadWriteCloser).Close() == base+11, "converted interface usable after GC")
	}
	finish("l06_iface_convert_matrix")
}
