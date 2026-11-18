// L17: chan struct{} 信号通道。worker 栈上持有指针链，经 nil payload 的
// ready/release/done 信号同步；main 在 worker 阻塞期间 GC，close 广播唤醒后再次校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	next *item
}

type holdSet struct {
	h    *item
	list []*item
	tag  string
}

const workers = 4

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func envRounds(def, max int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > max {
				return max
			}
			return v
		}
	}
	return def
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func makeChain(base int64, n int) *item {
	var head *item
	for i := 0; i < n; i++ {
		head = &item{v: base + int64(i) + 1, next: head}
	}
	return head
}

func chainSum(p *item) int64 {
	sum := int64(0)
	for ; p != nil; p = p.next {
		sum += p.v
	}
	return sum
}

func checkChain(p *item, base int64, n int, where string) bool {
	if p == nil {
		ck(false, fmt.Sprintf("%s: chain nil seed=%d", where, uafSeed))
		return false
	}
	idx := n
	for q := p; q != nil; q = q.next {
		if q.v != base+int64(idx) {
			ck(false, fmt.Sprintf("%s: node %d v=%d want=%d seed=%d", where, idx, q.v, base+int64(idx), uafSeed))
			return false
		}
		idx--
	}
	if idx != 0 {
		ck(false, fmt.Sprintf("%s: chain length mismatch idx=%d seed=%d", where, idx, uafSeed))
		return false
	}
	return true
}

func chainWant(base int64, n int) int64 {
	sum := int64(0)
	for i := 1; i <= n; i++ {
		sum += base + int64(i)
	}
	return sum
}

//go:noinline
func worker(id int, base int64, ready, release, done chan struct{}, slot *int64) {
	// 通道元素为 struct{}：指针链只能存在于 worker 栈/局部堆对象中。
	p := makeChain(base, 8)
	hold := holdSet{h: p, list: []*item{p.next, p.next.next}, tag: "w"}
	ready <- struct{}{}
	<-release
	gcStress()
	ok := checkChain(p, base, 8, fmt.Sprintf("worker %d after release", id))
	if hold.list[0] == nil || hold.list[1] == nil || hold.tag != "w" {
		ck(false, fmt.Sprintf("worker %d: hold set corrupted seed=%d", id, uafSeed))
		ok = false
	} else if hold.list[0].v != base+7 || hold.list[1].v != base+6 {
		ck(false, fmt.Sprintf("worker %d: hold list values corrupted seed=%d", id, uafSeed))
		ok = false
	}
	gcStress()
	if !checkChain(p, base, 8, fmt.Sprintf("worker %d second GC", id)) {
		ok = false
	}
	if ok {
		*slot = chainSum(p) + int64(len(hold.tag))
	} else {
		*slot = -1
	}
	done <- struct{}{}
}

func main() {
	uafSeed = seedFromEnv()
	rounds := envRounds(24, 4096)

	for r := 0; r < rounds; r++ {
		ready := make(chan struct{})
		release := make(chan struct{})
		done := make(chan struct{})
		slots := make([]int64, workers)
		bases := make([]int64, workers)

		for i := 0; i < workers; i++ {
			bases[i] = int64(r*1000) + int64(i*100) + uafSeed%97
			go worker(i, bases[i], ready, release, done, &slots[i])
		}
		for i := 0; i < workers; i++ {
			<-ready
		}
		// 所有 worker 都持有指针链，正处于或即将进入 <-release 阻塞。
		gcStress()
		close(release) // chan struct{} 广播：所有接收者同时被唤醒。
		for i := 0; i < workers; i++ {
			<-done
		}
		gcStress()
		for i := 0; i < workers; i++ {
			want := chainWant(bases[i], 8) + int64(len("w"))
			if slots[i] != want {
				ck(false, fmt.Sprintf("round %d worker %d: slot=%d want=%d seed=%d", r, i, slots[i], want, uafSeed))
			}
		}
	}

	// 关闭广播：多个等待者同时从同一个已关闭的 chan struct{} 醒来。
	closed := make(chan struct{})
	acks := make(chan struct{}, 3)
	var sums [3]int64
	for i := 0; i < 3; i++ {
		go func(i int) {
			base := int64(70000 + i*50)
			p := makeChain(base, 6)
			<-closed
			gcStress()
			if checkChain(p, base, 6, fmt.Sprintf("closed wait %d", i)) {
				sums[i] = chainSum(p)
			}
			acks <- struct{}{}
		}(i)
	}
	gcStress()
	close(closed)
	for i := 0; i < 3; i++ {
		<-acks
	}
	gcStress()
	for i := 0; i < 3; i++ {
		want := chainWant(int64(70000+i*50), 6)
		if sums[i] != want {
			ck(false, fmt.Sprintf("closed wait %d: sum=%d want=%d seed=%d", i, sums[i], want, uafSeed))
		}
	}

	finish("l17_chan_struct_signal")
}
