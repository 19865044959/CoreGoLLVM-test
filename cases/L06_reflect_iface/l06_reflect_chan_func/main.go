// L06: reflect chan/func：Send/Recv/Select/Try*/Close、MakeFunc/Call/CallSlice 跨 GC。
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

var (
	failed  int32
	gcSink  [][]byte
	fnSink  []reflect.Value
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
	const rounds = 12
	ct := reflect.TypeOf((chan *node)(nil))
	it := reflect.TypeOf((chan int64)(nil))

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*71))
		n := 3 + rnd.Intn(4)

		ch := reflect.MakeChan(ct, 8)
		ck(ch.Cap() == 8 && ch.Len() == 0, "new chan shape")
		values := make([]int64, n)
		for i := 0; i < n; i++ {
			values[i] = int64(r*1000 + i*13)
			ch.Send(reflect.ValueOf(&node{V: values[i], Label: fmt.Sprintf("m%d", i)}))
		}
		ck(ch.Len() == n, "buffered sends increase len")
		// 发送方局部指针已丢弃，仅靠 chan 缓冲区在 GC 后保活。
		gcStress()
		for i := 0; i < n; i++ {
			v, ok := ch.Recv()
			ck(ok && v.Elem().FieldByName("V").Int() == values[i], "chan recv value after GC")
			ck(v.Elem().FieldByName("Label").String() == fmt.Sprintf("m%d", i), "chan recv label after GC")
		}
		ch.Close()
		z, ok := ch.Recv()
		ck(!ok && z.IsValid() && z.IsNil(), "recv on closed empty chan")
		func() {
			defer func() { ck(recover() != nil, "send on closed chan panics") }()
			ch.Send(reflect.ValueOf(&node{V: 1}))
		}()
		func() {
			defer func() { ck(recover() != nil, "close of closed chan panics") }()
			ch.Close()
		}()

		small := reflect.MakeChan(ct, 1)
		ck(small.TrySend(reflect.ValueOf(&node{V: 91, Label: "t"})), "TrySend into empty chan")
		ck(!small.TrySend(reflect.ValueOf(&node{V: 92})), "TrySend into full chan")
		tv, tok := small.TryRecv()
		ck(tok && tv.Elem().FieldByName("V").Int() == 91, "TryRecv gets value")
		tv2, tok2 := small.TryRecv()
		ck(!tok2 && !tv2.IsValid(), "TryRecv on empty returns invalid zero Value")

		rc1 := reflect.MakeChan(ct, 1)
		rc1.Send(reflect.ValueOf(&node{V: 31, Label: "sel"}))
		rc2 := reflect.MakeChan(ct, 1)
		sc := reflect.MakeChan(it, 1)
		chosen, recvd, recvOK := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: rc1},
			{Dir: reflect.SelectRecv, Chan: rc2},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 0 && recvOK && recvd.Elem().FieldByName("V").Int() == 31, "reflect.Select picks ready recv")
		chosenSend, _, _ := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: sc, Send: reflect.ValueOf(int64(8))},
			{Dir: reflect.SelectDefault},
		})
		ck(chosenSend == 0 && sc.Len() == 1, "select takes ready send")
		chosen2, _, _ := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: sc, Send: reflect.ValueOf(int64(9))},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen2 == 1, "select default on full send chan")
		sc.Recv()

		closed := reflect.MakeChan(ct, 0)
		closed.Close()
		chosen3, zr, zrOK := reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: closed}})
		ck(chosen3 == 0 && !zrOK && zr.IsValid() && zr.IsNil(), "select on closed chan returns zero")

		ca := reflect.MakeChan(reflect.TypeOf((chan any)(nil)), 2)
		ca.Send(reflect.ValueOf(any(&node{V: 55, Label: "any"})))
		ca.Send(reflect.ValueOf(any("text")))
		gcStress()
		av, aok := ca.Recv()
		gotNode, nodeOK := av.Interface().(*node)
		ck(aok && nodeOK && gotNode.V == 55 && gotNode.Label == "any", "chan any pointer element after GC")
		as, aok2 := ca.Recv()
		ck(aok2 && as.Interface().(string) == "text", "chan any string element")

		// MakeFunc 闭包捕获指针，GC 后继续调用。
		captured := &node{V: 41, Label: "cap"}
		fnType := reflect.TypeOf(func(int64) int64 { return 0 })
		fnv := reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
			return []reflect.Value{reflect.ValueOf(args[0].Int() + captured.V)}
		})
		fnSink = append(fnSink[:0], fnv)
		fl := fnv.Interface().(func(int64) int64)
		anySink = fl
		ck(fnv.Type() == fnType, "MakeFunc type identity")
		ck(fnv.Call([]reflect.Value{reflect.ValueOf(int64(1))})[0].Int() == 42, "MakeFunc call")
		ck(fl(2) == 43, "MakeFunc interface call")
		gcStress()
		ck(fnSink[0].Call([]reflect.Value{reflect.ValueOf(int64(3))})[0].Int() == 44, "MakeFunc closure survives GC")
		ck(anySink.(func(int64) int64)(4) == 45, "func value in interface survives GC")
		ck(captured.Label == "cap", "captured pointer survives GC")
		func() {
			defer func() { ck(recover() != nil, "Call with wrong arity panics") }()
			fnv.Call(nil)
		}()

		vfType := reflect.TypeOf(func(int64, ...int64) int64 { return 0 })
		vf := reflect.MakeFunc(vfType, func(args []reflect.Value) []reflect.Value {
			sum := args[0].Int()
			rest := args[1]
			for i := 0; i < rest.Len(); i++ {
				sum += rest.Index(i).Int()
			}
			return []reflect.Value{reflect.ValueOf(sum)}
		})
		ck(vf.Call([]reflect.Value{reflect.ValueOf(int64(1)), reflect.ValueOf(int64(2)), reflect.ValueOf(int64(3))})[0].Int() == 6, "variadic MakeFunc Call")
		ck(vf.CallSlice([]reflect.Value{reflect.ValueOf(int64(4)), reflect.ValueOf([]int64{5, 6})})[0].Int() == 15, "variadic MakeFunc CallSlice")

		f1 := func(x int64) int64 { return x * 2 }
		f2 := f1
		ck(reflect.ValueOf(f1).Pointer() == reflect.ValueOf(f2).Pointer(), "same func variable has same code pointer")
		ck(reflect.ValueOf(f1).Pointer() != 0, "func pointer is non-zero")

		gcStress()
		ck(fnv.Kind() == reflect.Func && fnSink[0].Kind() == reflect.Func, "reflect func Value kind after GC")
	}
	finish("l06_reflect_chan_func")
}
