// L06: reflect.Select：nil/关闭/满通道、default、唯一就绪索引、多就绪语义、panic 路径，跨 GC。
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
	V    int64
	Name string
}

var (
	failed   int32
	gcSink   [][]byte
	chanSink []reflect.Value
	uafSeed  int64 = 1
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 30)
	nodeCh := reflect.TypeOf((chan *node)(nil))
	intCh := reflect.TypeOf((chan int64)(nil))
	anyCh := reflect.TypeOf((chan any)(nil))
	nilChan := reflect.Value{}

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*211))
		seedVal := int64(r*100 + rnd.Intn(97))

		// 1) nil recv 永不就绪；无其他分支时走 default。
		chosen, rv, ok := reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: nilChan},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 1 && !ok && !rv.IsValid(), "nil recv selects default")

		// 2) nil send 永不就绪。
		chosen, _, _ = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: nilChan, Send: reflect.ValueOf(&node{V: seedVal})},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 1, "nil send selects default")

		// 3) 仅 chan 缓冲区持有元素指针，GC 后经 Select 取回。
		ch := reflect.MakeChan(nodeCh, 4)
		ch.Send(reflect.ValueOf(&node{V: seedVal, Name: fmt.Sprintf("n%d", r)}))
		chanSink = append(chanSink[:0], ch)
		ch = nilChan
		gcStress()
		ch = chanSink[0]
		chosen, rv, ok = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: ch},
			{Dir: reflect.SelectRecv, Chan: nilChan},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 0 && ok && rv.IsValid(), "buffered recv after GC")
		got := rv.Interface().(*node)
		ck(got != nil && got.V == seedVal && got.Name == fmt.Sprintf("n%d", r), "received pointer after GC")

		// 4) 关闭通道 recv 恒就绪：返回值零值且 ok=false，并优先于 default。
		closed := reflect.MakeChan(nodeCh, 0)
		closed.Close()
		chosen, rv, ok = reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: closed}})
		ck(chosen == 0 && !ok && rv.IsValid() && rv.IsNil(), "closed recv returns zero pointer")
		chosen, _, ok = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: closed},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 0 && !ok, "closed recv beats default")

		// 5) 就绪 send 优先于 default；发送后值可读。
		snd := reflect.MakeChan(intCh, 2)
		chosen, _, _ = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: snd, Send: reflect.ValueOf(seedVal)},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 0 && snd.Len() == 1, "ready send selected")
		if v, ok := snd.Recv(); !ok || v.Int() != seedVal {
			ck(false, "send value enqueued")
		}

		// 6) 满 send 通道不阻塞，走 default。
		full := reflect.MakeChan(intCh, 1)
		full.Send(reflect.ValueOf(int64(1)))
		chosen, _, _ = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: full, Send: reflect.ValueOf(int64(2))},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 1 && full.Len() == 1, "full send selects default")

		// 7) 只有 default 的 Select 立即返回。
		chosen, rv, ok = reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectDefault}})
		ck(chosen == 0 && !ok && !rv.IsValid(), "default-only select")

		// 8) 多个 nil 分支中仅一个就绪：选中索引确定。
		ready := reflect.MakeChan(intCh, 1)
		ready.Send(reflect.ValueOf(seedVal + 5))
		chosen, rv, ok = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: nilChan},
			{Dir: reflect.SelectRecv, Chan: ready},
			{Dir: reflect.SelectRecv, Chan: nilChan},
			{Dir: reflect.SelectSend, Chan: nilChan, Send: reflect.ValueOf(int64(9))},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 1 && ok && rv.Int() == seedVal+5, "single ready recv index")

		// 9) 两个 recv 同时就绪：只能断言命中其中之一，且两个值都被消耗。
		mr1 := reflect.MakeChan(intCh, 1)
		mr1.Send(reflect.ValueOf(int64(11)))
		mr2 := reflect.MakeChan(intCh, 1)
		mr2.Send(reflect.ValueOf(int64(22)))
		chosen, rv, ok = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: mr1},
			{Dir: reflect.SelectRecv, Chan: mr2},
			{Dir: reflect.SelectDefault},
		})
		ck(ok && (chosen == 0 || chosen == 1), "multi-ready selects a ready case")
		gotVal := rv.Int()
		ck(gotVal == 11 || gotVal == 22, "multi-ready value from a ready case")
		total := gotVal
		for mr1.Len() > 0 || mr2.Len() > 0 {
			if mr1.Len() > 0 {
				v, _ := mr1.Recv()
				total += v.Int()
			}
			if mr2.Len() > 0 {
				v, _ := mr2.Recv()
				total += v.Int()
			}
		}
		ck(total == 33, "all multi-ready values consumed")

		// 10) chan any：接口值经 Select 收发。
		ac := reflect.MakeChan(anyCh, 2)
		ac.Send(reflect.ValueOf(&node{V: seedVal + 1, Name: "any"}))
		ac.Send(reflect.ValueOf("txt"))
		chosen, rv, ok = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectRecv, Chan: ac},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 0 && ok, "chan any first recv")
		if n, ok := rv.Interface().(*node); ok {
			ck(n.V == seedVal+1 && n.Name == "any", "chan any pointer value")
		} else {
			ck(false, "chan any dynamic type")
		}
		chosen, rv, ok = reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectRecv, Chan: ac}})
		if s, ok := rv.Interface().(string); ok {
			ck(chosen == 0 && ok && s == "txt", "chan any string value")
		} else {
			ck(false, "chan any string dynamic type")
		}

		// 11) 就绪分支位置在 0..3 之间轮转，case 数量固定为 5。
		for k := 0; k < 64; k++ {
			rc := reflect.MakeChan(intCh, 1)
			rc.Send(reflect.ValueOf(int64(k)))
			idx := k % 4
			cases := make([]reflect.SelectCase, 5)
			for j := 0; j < 4; j++ {
				cases[j] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: nilChan}
			}
			cases[idx] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: rc}
			cases[4] = reflect.SelectCase{Dir: reflect.SelectDefault}
			chosen, rv, ok := reflect.Select(cases)
			ck(chosen == idx && ok && rv.Int() == int64(k), "varied case position")
		}

		// 12) 经 Select 发送的指针在 chan 缓冲区中跨 GC 保活。
		out := reflect.MakeChan(nodeCh, 1)
		chosen, _, _ = reflect.Select([]reflect.SelectCase{
			{Dir: reflect.SelectSend, Chan: out, Send: reflect.ValueOf(&node{V: seedVal + 2, Name: "sent"})},
			{Dir: reflect.SelectDefault},
		})
		ck(chosen == 0 && out.Len() == 1, "select send pointer")
		chanSink = append(chanSink, out)
		gcStress()
		v, ok := out.Recv()
		sv := v.Interface().(*node)
		ck(ok && sv != nil && sv.V == seedVal+2 && sv.Name == "sent", "select-sent pointer after GC")
	}

	// panic 路径。
	mustPanic("send value wrong type", func() {
		c := reflect.MakeChan(nodeCh, 1)
		reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectSend, Chan: c, Send: reflect.ValueOf(int64(1))}})
	})
	mustPanic("send missing value", func() {
		c := reflect.MakeChan(nodeCh, 1)
		reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectSend, Chan: c}})
	})
	mustPanic("send on closed channel", func() {
		c := reflect.MakeChan(nodeCh, 1)
		c.Close()
		reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectSend, Chan: c, Send: reflect.ValueOf(&node{V: 1})}})
	})
	mustPanic("invalid select direction", func() {
		c := reflect.MakeChan(nodeCh, 1)
		reflect.Select([]reflect.SelectCase{{Dir: reflect.SelectDir(9), Chan: c}})
	})

	finish("l06_reflect_select_matrix")
}
