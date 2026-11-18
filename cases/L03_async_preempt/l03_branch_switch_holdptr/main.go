// L03: 长循环内用 switch/if 多分支包裹指针解引用，分支体持针跨并发 GC；
// 循环回边与分支汇合处是异步抢占点，校验和与分支计数必须确定。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed int32
	gcKeep [][]byte
)

const (
	bSlots    = 64
	bMinIters = 1 << 17
	bCapIters = 1 << 24
)

type bNode struct {
	v       int64
	tagEven bool
	next    *bNode
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 1
}

func bIdxAt(i, step, off int64) int {
	return int((i*step + off) % bSlots)
}

// 只做整数推算，不读取任何堆指针，作为循环结果的参考值。
func bExpected(i int64, idx int) int64 {
	v := int64(idx)*2 + 3
	nextV := int64((idx+1)%bSlots)*2 + 3
	switch i & 3 {
	case 0:
		return v
	case 1:
		return v + nextV
	case 2:
		if idx%2 == 0 {
			return v * 2
		}
		return v*2 + 1
	default:
		switch (v + i) & 3 {
		case 0:
			return v + 4
		case 1:
			return v + 5
		case 2:
			return v + 6
		default:
			return v + 7
		}
	}
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(16) + 1)
	off := int64(rng.Intn(bSlots))

	backing := make([]bNode, bSlots)
	for i := range backing {
		backing[i].v = int64(i)*2 + 3
		backing[i].tagEven = i%2 == 0
	}
	for i := range backing {
		backing[i].next = &backing[(i+1)%bSlots]
	}

	var epoch atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for r := 0; r < 8; r++ {
			junk := make([][]byte, 8)
			for i := range junk {
				junk[i] = make([]byte, 8192)
				junk[i][0] = byte(r + i)
			}
			gcKeep = junk
			runtime.GC()
			epoch.Add(1)
		}
		gcKeep = nil
	}()
	close(start)

	var sum int64
	var done int64
	for (epoch.Load() < 1 || done < bMinIters) && done < bCapIters {
		idx := bIdxAt(done, step, off)
		p := &backing[idx]
		var add int64
		switch done & 3 {
		case 0:
			add = p.v
		case 1:
			q := p.next
			if q != nil {
				add = p.v + q.v
			} else {
				add = -1 << 30
			}
		case 2:
			if p.tagEven {
				add = p.v * 2
			} else {
				add = p.v*2 + 1
			}
		default:
			switch (p.v + done) & 3 {
			case 0:
				add = p.v + 4
			case 1:
				add = p.v + 5
			case 2:
				add = p.v + 6
			default:
				add = p.v + 7
			}
		}
		sum += add
		done++
	}
	now := epoch.Load()
	wg.Wait()

	var expected int64
	for i := int64(0); i < done; i++ {
		expected += bExpected(i, bIdxAt(i, step, off))
	}

	ck(now >= 1, fmt.Sprintf("GC completed during branch/switch loop (seed=%d)", seed))
	ck(done >= bMinIters, fmt.Sprintf("branch loop executed (seed=%d)", seed))
	ck(sum == expected, fmt.Sprintf("branch/switch checksum (seed=%d)", seed))

	corrupt := false
	for i := range backing {
		if backing[i].v != int64(i)*2+3 || backing[i].tagEven != (i%2 == 0) ||
			backing[i].next != &backing[(i+1)%bSlots] {
			corrupt = true
		}
	}
	ck(!corrupt, fmt.Sprintf("branch-loop nodes intact after GC (seed=%d)", seed))
	runtime.KeepAlive(backing)
	finish("l03_branch_switch_holdptr")
}
