// L04: close 广播级联：root close 唤醒 4 个 relay，relay 用 sync.Once 幂等关闭各自 child，
// 再广播唤醒 32 个 waiter；全部 waiter 阻塞在 child 上时 GC，唤醒后复验私有指针链。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type bnode struct {
	v    int64
	next *bnode
}

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...any) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func runCascade(seed int64) {
	const relays = 4
	const perRelay = 8
	const total = relays * perRelay

	root := make(chan struct{})
	children := make([]chan struct{}, relays)
	ready := make(chan int, total)
	res := make(chan int, total)
	keep := make([]*bnode, total)
	var bad int32
	var relayWG, waiterWG sync.WaitGroup

	relayWG.Add(relays)
	for r := 0; r < relays; r++ {
		children[r] = make(chan struct{})
		go func(r int) {
			defer relayWG.Done()
			<-root
			var once sync.Once
			// 幂等 close：同一个 child 允许被重复请求关闭。
			for k := 0; k < 3; k++ {
				once.Do(func() { close(children[r]) })
				runtime.Gosched()
			}
		}(r)
	}

	waiterWG.Add(total)
	for w := 0; w < total; w++ {
		go func(w int) {
			defer waiterWG.Done()
			b := seed*1009 + int64(w)*13
			head := &bnode{v: b, next: &bnode{v: b + 1, next: &bnode{v: b + 2}}}
			keep[w] = head
			ready <- w
			// 让出若干次，确保广播前多数 waiter 已停在 child 接收上。
			for i := 0; i < 16; i++ {
				runtime.Gosched()
			}
			<-children[w/perRelay]
			if head.next == nil || head.next.next == nil ||
				head.next.v != b+1 || head.next.next.v != b+2 {
				atomic.AddInt32(&bad, 1)
			}
			// close 之后 channel 永久就绪，再次接收不阻塞。
			select {
			case <-children[w/perRelay]:
			default:
				atomic.AddInt32(&bad, 1)
			}
			res <- w
		}(w)
	}

	for i := 0; i < total; i++ {
		<-ready
	}
	gcStress()
	close(root)
	relayWG.Wait()
	waiterWG.Wait()
	close(res)

	seen := make([]int32, total)
	for w := range res {
		if w < 0 || w >= total {
			atomic.AddInt32(&bad, 1)
			continue
		}
		seen[w]++
	}
	for i := 0; i < total; i++ {
		ckf(seen[i] == 1, "waiter %d woke %d times want=1", i, seen[i])
	}
	ckf(atomic.LoadInt32(&bad) == 0, "waiter chain corrupted or child not closed")

	for r := 0; r < relays; r++ {
		select {
		case <-children[r]:
		default:
			ckf(false, "child %d not closed after cascade", r)
		}
	}

	gcStress()
	for w := 0; w < total; w++ {
		h := keep[w]
		if h == nil || h.next == nil || h.next.next == nil {
			ckf(false, "waiter %d chain missing after final GC", w)
			continue
		}
		b := seed*1009 + int64(w)*13
		ckf(h.v == b && h.next.v == b+1 && h.next.next.v == b+2,
			"waiter %d chain corrupted after final GC", w)
	}
}

func main() {
	seed := envSeed()
	runCascade(seed)
	finish("l04_close_broadcast_cascade")
}
