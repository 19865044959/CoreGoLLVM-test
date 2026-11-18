// L04: goroutine 内 panic/recover：panic 值携带堆指针，跨越 5 层栈帧展开；
// recover 后阻塞并由主协程 GC，再校验链与 panic payload 中的指针身份。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type node struct {
	v    int64
	next *node
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

type panicPayload struct {
	id  int
	ptr *node
	sum int64
}

func buildChain(base int64) []*node {
	nodes := make([]*node, 4)
	for i := len(nodes) - 1; i >= 0; i-- {
		nodes[i] = &node{v: base + int64(i)*11}
		if i+1 < len(nodes) {
			nodes[i].next = nodes[i+1]
		}
	}
	return nodes
}

func chainSum(chain []*node) int64 {
	var sum int64
	for _, n := range chain {
		sum += n.v
	}
	return sum
}

func slotSum(slots []*node) int64 {
	var sum int64
	for _, n := range slots {
		sum += n.v
	}
	return sum
}

//go:noinline
func panicChain(depth, id int, chain []*node) int64 {
	var frame [64]*node
	for i := range frame {
		frame[i] = chain[(i+depth)&3]
	}
	if depth == 4 {
		ptr := chain[(depth+id)&3]
		switch {
		case id%5 == 0:
			panic(fmt.Sprintf("worker-%d", id))
		case id%7 == 0:
			panic(fmt.Errorf("worker-%d error", id))
		default:
			panic(panicPayload{id: id, ptr: ptr, sum: chainSum(chain)})
		}
	}
	// 回溯点：defer 中的 recover 必须让各层 frame 得到正确的栈扫描。
	return panicChain(depth+1, id, chain) + slotSum(frame[:])
}

func recoverWorker(id int, chain []*node) (bad int32) {
	want := chainSum(chain)
	defer func() {
		r := recover()
		if r == nil {
			bad = 1
			return
		}
		switch v := r.(type) {
		case panicPayload:
			if v.id != id || v.ptr != chain[(4+id)&3] || v.ptr.v != chain[(4+id)&3].v || v.sum != want {
				bad = 1
			}
		case string:
			if v != fmt.Sprintf("worker-%d", id) {
				bad = 1
			}
		case error:
			if v.Error() != fmt.Sprintf("worker-%d error", id) {
				bad = 1
			}
		default:
			bad = 1
		}
		if chainSum(chain) != want {
			bad = 1
		}
	}()
	panicChain(0, id, chain)
	return 1 // 不可达
}

type panicResult struct {
	id  int
	bad int32
	sum int64
}

func main() {
	seed := envSeed()
	const workers = 24

	ready := make(chan int, workers)
	release := make(chan struct{})
	out := make(chan panicResult, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for id := 0; id < workers; id++ {
		go func(id int) {
			defer wg.Done()
			base := int64(id)*1000 + seed*43
			chain := buildChain(base)
			bad := recoverWorker(id, chain)
			ready <- id
			<-release
			// recover 结束后链只挂在 goroutine 栈/寄存器上，GC 后再验证。
			want := base*4 + 66
			if chainSum(chain) != want {
				bad = 1
			}
			out <- panicResult{id: id, bad: bad, sum: chainSum(chain)}
		}(id)
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	gcStress()
	close(release)
	wg.Wait()
	close(out)

	seen := make([]bool, workers)
	var total, wantTotal int64
	for r := range out {
		if r.id < 0 || r.id >= workers {
			ckf(false, "bad worker id %d", r.id)
			continue
		}
		seen[r.id] = true
		base := int64(r.id)*1000 + seed*43
		want := base*4 + 66
		ckf(r.bad == 0, "worker %d recover/payload/chain broken", r.id)
		ckf(r.sum == want, "worker %d sum=%d want=%d", r.id, r.sum, want)
		total += r.sum
		wantTotal += want
	}
	for id, ok := range seen {
		ckf(ok, "worker %d missing result", id)
	}
	ckf(total == wantTotal, "panic total sum=%d want=%d", total, wantTotal)
	finish("l04_goroutine_panic_recover")
}
