// L06: reflect 方向通道与动态函数签名：ChanOf/ChanDir/Convert、方向受限的 Send/Recv/Select、
// FuncOf/MakeFunc 动态签名与 nil func，跨 GC 校验。
package main

import (
	"fmt"
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
	fnSink  []any
	valSink []reflect.Value
	uafSeed int64 = 1
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

	intT := reflect.TypeOf(0)
	strT := reflect.TypeOf("")
	boolT := reflect.TypeOf(false)
	elemT := reflect.TypeOf((*node)(nil))

	bothT := reflect.ChanOf(reflect.BothDir, elemT)
	sendT := reflect.ChanOf(reflect.SendDir, elemT)
	recvT := reflect.ChanOf(reflect.RecvDir, elemT)
	ck(bothT == reflect.TypeOf((chan *node)(nil)), "ChanOf both matches literal chan type")
	ck(sendT == reflect.TypeOf((chan<- *node)(nil)), "ChanOf send matches literal chan type")
	ck(recvT == reflect.TypeOf((<-chan *node)(nil)), "ChanOf recv matches literal chan type")
	ck(bothT.ChanDir() == reflect.BothDir, "both dir")
	ck(sendT.ChanDir() == reflect.SendDir, "send dir")
	ck(recvT.ChanDir() == reflect.RecvDir, "recv dir")
	ck(sendT.Elem() == elemT && recvT.Elem() == elemT, "directional elem type")
	ck(bothT.ConvertibleTo(sendT) && bothT.ConvertibleTo(recvT), "bidirectional converts to directional")
	ck(!sendT.ConvertibleTo(bothT) && !recvT.ConvertibleTo(bothT), "directional cannot convert back")
	ck(bothT.AssignableTo(sendT) && bothT.AssignableTo(recvT), "bidirectional assignable to directional")
	ck(!sendT.AssignableTo(bothT) && !recvT.AssignableTo(bothT), "directional not assignable back")
	ck(reflect.ChanOf(reflect.BothDir, elemT) == bothT, "ChanOf identity is stable")

	funcT := reflect.FuncOf([]reflect.Type{intT, strT}, []reflect.Type{intT, boolT}, false)
	litT := reflect.TypeOf(func(int, string) (int, bool) { return 0, false })
	ck(funcT == litT, "FuncOf matches literal signature")
	ck(!funcT.IsVariadic(), "FuncOf non-variadic")
	ck(funcT.NumIn() == 2 && funcT.In(0) == intT && funcT.In(1) == strT, "FuncOf inputs")
	ck(funcT.NumOut() == 2 && funcT.Out(0) == intT && funcT.Out(1) == boolT, "FuncOf outputs")
	varT := reflect.FuncOf([]reflect.Type{intT, reflect.SliceOf(intT)}, []reflect.Type{intT}, true)
	ck(varT == reflect.TypeOf(func(int, ...int) int { return 0 }), "FuncOf variadic identity")
	ck(varT.IsVariadic(), "FuncOf variadic flag")
	ck(varT != reflect.TypeOf(func(int, []int) int { return 0 }), "variadic differs from slice parameter")

	offset := uafSeed%7 + 1
	fnV := reflect.MakeFunc(funcT, func(args []reflect.Value) []reflect.Value {
		n := int(args[0].Int()+int64(len(args[1].String()))) + int(offset)
		return []reflect.Value{reflect.ValueOf(n), reflect.ValueOf(len(args[1].String())%2 == 0)}
	})
	fnSink = append(fnSink[:0], fnV.Interface())
	valSink = append(valSink[:0], fnV)
	out := fnV.Call([]reflect.Value{reflect.ValueOf(10), reflect.ValueOf("abcd")})
	ck(out[0].Int() == int64(10+4)+offset && out[1].Bool(), "dynamic func result")
	gcStress()
	out2 := valSink[0].Call([]reflect.Value{reflect.ValueOf(3), reflect.ValueOf("x")})
	ck(out2[0].Int() == int64(3+1)+offset && !out2[1].Bool(), "dynamic func after GC")
	typed := fnSink[0].(func(int, string) (int, bool))
	tv, tb := typed(2, "ab")
	ck(tv == 2+2+int(offset) && tb, "dynamic func interface view")

	var nilFn func(int64) int64
	nv := reflect.ValueOf(nilFn)
	ck(nv.IsValid() && nv.Kind() == reflect.Func && nv.IsNil(), "nil func value shape")
	mustPanic("call nil func", func() {
		nv.Call([]reflect.Value{reflect.ValueOf(int64(1))})
	})
	var nilAny any = nilFn
	ck(nilAny != nil, "typed nil func boxes non-nil interface")
	ck(reflect.ValueOf(nilAny).IsNil(), "boxed nil func is nil")
	mustPanic("call boxed nil func", func() {
		nilAny.(func(int64) int64)(1)
	})
	gcStress()
	ck(reflect.ValueOf(fnSink[0]).Kind() == reflect.Func, "func kind after GC")
	ck(reflect.ValueOf(nilAny).IsNil(), "boxed nil func still nil after GC")

	for r := 0; r < rounds; r++ {
		base := uafSeed*1000 + int64(r)*100
		ch := make(chan *node, 8)
		sv := reflect.ValueOf(ch).Convert(sendT)
		rv := reflect.ValueOf(ch).Convert(recvT)
		ck(sv.Type() == sendT && rv.Type() == recvT, "directional value types")

		n := 3 + int((uafSeed+int64(r))%3)
		for i := 0; i < n; i++ {
			sv.Send(reflect.ValueOf(&node{V: base + int64(i), Label: fmt.Sprintf("d%d", i)}))
		}
		ck(len(ch) == n, "send-only sends land in channel")
		ck(sv.TrySend(reflect.ValueOf(&node{V: base + 99, Label: "try"})), "TrySend on send-only")
		ck(len(ch) == n+1, "TrySend appended")
		mustPanic("recv on send-only", func() { sv.Recv() })
		mustPanic("TryRecv on send-only", func() { sv.TryRecv() })
		mustPanic("send on recv-only", func() { rv.Send(reflect.ValueOf(&node{})) })
		mustPanic("TrySend on recv-only", func() { rv.TrySend(reflect.ValueOf(&node{})) })

		gcStress()
		for i := 0; i < n; i++ {
			v, ok := rv.Recv()
			ck(ok && v.Elem().FieldByName("V").Int() == base+int64(i), "recv-only value after GC")
			ck(v.Elem().FieldByName("Label").String() == fmt.Sprintf("d%d", i), "recv-only label after GC")
		}
		tv, tok := rv.TryRecv()
		ck(tok && tv.Elem().FieldByName("V").Int() == base+99, "TryRecv on recv-only")
		_, tok2 := rv.TryRecv()
		ck(!tok2, "TryRecv empty recv-only")

		ch2 := make(chan int64, 1)
		int64T := reflect.TypeOf(int64(0))
		s2 := reflect.ValueOf(ch2).Convert(reflect.ChanOf(reflect.SendDir, int64T))
		idx, _, _ := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: s2, Send: reflect.ValueOf(int64(7))},
		})
		ck(idx == 0 && len(ch2) == 1, "SelectSend with send-only value")
		r2 := reflect.ValueOf(ch2).Convert(reflect.ChanOf(reflect.RecvDir, int64T))
		idx2, v2, ok2 := reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: r2}})
		ck(idx2 == 0 && ok2 && v2.Int() == 7, "SelectRecv with recv-only value")
		mustPanic("SelectSend with recv-only", func() {
			reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectSend, Chan: r2, Send: reflect.ValueOf(int64(1))}})
		})
		mustPanic("SelectRecv with send-only", func() {
			reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: s2}})
		})

		sv.Close()
		_, closedOK := rv.Recv()
		ck(!closedOK, "drain closed directional chan")
		mustPanic("close recv-only", func() { rv.Close() })

		fnT := reflect.TypeOf(func(int64) int64 { return 0 })
		fch := reflect.MakeChan(reflect.ChanOf(reflect.BothDir, fnT), 2)
		fch.Send(reflect.ValueOf(func(x int64) int64 { return x*2 + base }))
		fch.Send(reflect.ValueOf(func(x int64) int64 { return x - base }))
		frv := fch.Convert(reflect.ChanOf(reflect.RecvDir, fnT))
		gcStress()
		fv1, fok1 := frv.Recv()
		f1, ftyp1 := fv1.Interface().(func(int64) int64)
		ck(fok1 && ftyp1 && f1(3) == 6+base, "func in directional chan survives GC")
		fv2, fok2 := frv.Recv()
		f2, ftyp2 := fv2.Interface().(func(int64) int64)
		ck(fok2 && ftyp2 && f2(1) == 1-base, "second func in directional chan after GC")
	}
	finish("l06_reflect_directional_chan")
}
