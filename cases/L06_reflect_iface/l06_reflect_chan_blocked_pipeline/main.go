// L06: 反射通道/函数管线：goroutine 阻塞在 reflect Send/Recv 时 GC、chan of func、接口持有
// 闭包经通道往返、方向化通道转换与 MakeFunc 回调，全部跨显式 GC。
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

type fn = func(int64) int64

var (
	failed  int32
	gcSink  [][]byte
	chSink  []reflect.Value
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

func envInt(name string, def, min, max int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func chanType() reflect.Type {
	return reflect.TypeOf((chan fn)(nil))
}

func anyChanType() reflect.Type {
	return reflect.TypeOf((chan any)(nil))
}

// blockedRecvRunsGC 让消费者阻塞在 reflect.Value.Recv，主线程 GC 后再 Send 函数值。
func blockedRecvRunsGC(base int64) {
	ch := reflect.MakeChan(chanType(), 0)
	chSink = append(chSink, ch)
	ready := make(chan struct{})
	done := make(chan int64, 1)
	go func() {
		close(ready)
		v, ok := ch.Recv()
		if !ok {
			done <- -1
			return
		}
		f, ok := v.Interface().(fn)
		if !ok {
			done <- -2
			return
		}
		done <- f(10)
	}()
	<-ready
	runtime.Gosched()
	captured := &node{V: base, Label: "blocked-recv"}
	fv := reflect.MakeFunc(reflect.TypeOf(fn(nil)), func(args []reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(args[0].Int() + captured.V)}
	})
	gcStress()
	ch.Send(fv)
	ck(<-done == 10+base, "blocked reflect recv survives GC")
	ck(captured.V == base && captured.Label == "blocked-recv", "captured node after blocked recv GC")
	ch.Close()
}

// blockedSendRunsGC 让生产者在无缓冲 reflect chan 上阻塞，GC 后再接收。
func blockedSendRunsGC(base int64) {
	ch := reflect.MakeChan(chanType(), 0)
	chSink = append(chSink, ch)
	ready := make(chan struct{})
	go func() {
		close(ready)
		ch.Send(reflect.ValueOf(fn(func(x int64) int64 { return x*3 + base })))
	}()
	<-ready
	runtime.Gosched()
	gcStress()
	v, ok := ch.Recv()
	ck(ok, "blocked reflect send delivers after GC")
	if ok {
		f, fok := v.Interface().(fn)
		ck(fok && f(4) == 12+base, "blocked reflect send func call after GC")
	}
	ch.Close()
}

// blockedRecvClose 让接收者阻塞，随后 Close 唤醒并得到 ok=false。
func blockedRecvClose() {
	ch := reflect.MakeChan(chanType(), 0)
	chSink = append(chSink, ch)
	ready := make(chan struct{})
	res := make(chan bool, 1)
	go func() {
		close(ready)
		_, ok := ch.Recv()
		res <- ok
	}()
	<-ready
	runtime.Gosched()
	gcStress()
	ch.Close()
	ck(!<-res, "blocked reflect recv observes close")
}

// funcPipeline 覆盖 chan any、方向化函数通道与 Select。
func funcPipeline(base int64) {
	capNode := &node{V: base + 1, Label: "pipeline"}
	closure := fn(func(x int64) int64 { return x + capNode.V })
	ac := reflect.MakeChan(anyChanType(), 4)
	chSink = append(chSink, ac)
	ac.Send(reflect.ValueOf(any(closure)))
	ac.Send(reflect.ValueOf(any(capNode)))
	ac.Send(reflect.ValueOf(any("text")))
	ac.Send(reflect.Zero(reflect.TypeOf((*any)(nil)).Elem()))
	gcStress()
	v, ok := ac.Recv()
	f, fok := v.Interface().(fn)
	ck(ok && fok && f(5) == 5+base+1, "chan any func after GC")
	v, ok = ac.Recv()
	n, nok := v.Interface().(*node)
	ck(ok && nok && n.V == base+1 && n.Label == "pipeline", "chan any pointer after GC")
	v, ok = ac.Recv()
	ck(ok && v.Interface().(string) == "text", "chan any string after GC")
	v, ok = ac.Recv()
	ck(ok && v.Kind() == reflect.Interface && v.IsNil(), "chan any nil interface element")
	ac.Close()

	ch := reflect.MakeChan(chanType(), 1)
	chSink = append(chSink, ch)
	sendOnly := ch.Convert(reflect.TypeOf((chan<- fn)(nil)))
	recvOnly := ch.Convert(reflect.TypeOf((<-chan fn)(nil)))
	sendOnly.Send(reflect.ValueOf(fn(func(x int64) int64 { return x * 2 })))
	gcStress()
	rv, rok := recvOnly.Recv()
	rf, rfok := rv.Interface().(fn)
	ck(rok && rfok && rf(6) == 12, "directional func chan after GC")
	ch.Close()

	sel := reflect.MakeChan(chanType(), 1)
	chSink = append(chSink, sel)
	selNode := &node{V: base + 2, Label: "select"}
	sel.Send(reflect.MakeFunc(reflect.TypeOf(fn(nil)), func(args []reflect.Value) []reflect.Value {
		return []reflect.Value{reflect.ValueOf(args[0].Int() + selNode.V)}
	}))
	chosen, sv, sok := reflect.Select([]reflect.SelectCase{
		{Dir: reflect.SelectRecv, Chan: sel},
		{Dir: reflect.SelectDefault},
	})
	sf, sfok := sv.Interface().(fn)
	ck(chosen == 0 && sok && sfok && sf(1) == base+3, "reflect.Select func value after GC")
	ck(selNode.Label == "select", "select captured node after GC")
	sel.Close()
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 4, 1, 2000)
	for r := 0; r < rounds; r++ {
		base := uafSeed*100 + int64(r)*11
		blockedRecvRunsGC(base)
		blockedSendRunsGC(base + 1)
		blockedRecvClose()

		// 函数值本身装进接口，丢弃局部变量后仍经反射与直接调用可用。
		capNode := &node{V: base + 3, Label: "iface-fn"}
		f1 := fn(func(x int64) int64 { return x + capNode.V })
		anySink = any(f1)
		gcStress()
		f2, ok := anySink.(fn)
		ck(ok && f2(2) == base+5, "func value in interface after GC")
		ck(capNode.V == base+3 && capNode.Label == "iface-fn", "func closure node after GC")

		funcPipeline(base)
	}
	finish("l06_reflect_chan_blocked_pipeline")
}
