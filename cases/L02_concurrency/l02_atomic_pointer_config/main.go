// L02: atomic.Value 热切换不可变配置对象：单写者持续 Store 新对象，
// 多读者无锁 Load，校验对象内部一致性、代数单调与跨 GC 存活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	nStores     = 160
	nReaders    = 6
	readerLoops = 200
)

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
	cfg    atomic.Value // 存放 *config
)

type node struct {
	id  int64
	val int64
}

type config struct {
	gen   int64
	limit int
	nodes []*node
	blob  [4]int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	gcMu.Lock()
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	gcMu.Unlock()
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

func makeConfig(gen int64) *config {
	c := &config{gen: gen, limit: int(gen%97) + 1, nodes: make([]*node, 4)}
	for i := range c.nodes {
		c.nodes[i] = &node{id: int64(i), val: gen*10 + int64(i)}
	}
	for i := range c.blob {
		c.blob[i] = gen*100 + int64(i)
	}
	return c
}

func (c *config) valid() bool {
	if c == nil || c.gen <= 0 {
		return false
	}
	if c.limit != int(c.gen%97)+1 {
		return false
	}
	if len(c.nodes) != 4 {
		return false
	}
	for i, n := range c.nodes {
		if n == nil || n.id != int64(i) || n.val != c.gen*10+int64(i) {
			return false
		}
		if c.blob[i] != c.gen*100+int64(i) {
			return false
		}
	}
	return true
}

func reader(id int, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	last := int64(0)
	loops := 0
	for i := 0; i < readerLoops; i++ {
		cur, _ := cfg.Load().(*config)
		if !cur.valid() {
			ck(false, "reader loaded inconsistent config")
			continue
		}
		if cur.gen < last {
			ck(false, fmt.Sprintf("config generation regressed from %d to %d", last, cur.gen))
		}
		last = cur.gen
		loops++
		if i%25 == 0 {
			gcStress()
			if !cur.valid() {
				ck(false, "config snapshot corrupted after GC")
			}
			if cur.gen < last {
				ck(false, "config generation regressed across GC")
			}
		}
	}
	ck(loops == readerLoops, "reader loop count mismatch")
}

func main() {
	cfg.Store(makeConfig(1))

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func(id int) {
		defer wg.Done()
		<-start
		for gen := int64(2); gen <= nStores; gen++ {
			c := makeConfig(gen)
			if gen%17 == 0 {
				gcStress()
				if !c.valid() {
					ck(false, "new config corrupted by GC before store")
				}
			}
			cfg.Store(c)
		}
	}(0)
	for i := 0; i < nReaders; i++ {
		wg.Add(1)
		go reader(i, start, &wg)
	}
	close(start)
	wg.Wait()

	final, _ := cfg.Load().(*config)
	ck(final.valid(), "final config invalid")
	ck(final.gen == nStores, fmt.Sprintf("final generation got=%d want=%d", final.gen, int64(nStores)))
	runtime.KeepAlive(final)

	fmt.Printf("stores=%d readers=%d final_gen=%d\n", nStores, nReaders, final.gen)
	finish("l02_atomic_pointer_config")
}
