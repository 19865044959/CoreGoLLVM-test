// L02: sync.Once 并发初始化只执行一次 + sync.Pool 的归还清理与 GC 后复用；
// 多 worker 并发 Get/使用/GC/Put，验证池化对象不携带上一轮引用。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

const (
	nInitGoroutines = 32
	nPoolWorkers    = 16
	poolRounds      = 96
	tableSize       = 64
)

var (
	failed    int32
	gcMu      sync.Mutex
	gcSink    [][]byte
	reg       registry
	initCount int32
	newCount  int32
)

type entry struct {
	key  int
	val  int64
	name string
}

type blob struct {
	tag  int64
	name string
}

type scratch struct {
	used bool
	p    *blob
	buf  []byte
}

type registry struct {
	once sync.Once
	data map[int]*entry
	pool *sync.Pool
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

func (r *registry) init() {
	atomic.AddInt32(&initCount, 1)
	r.data = make(map[int]*entry, tableSize)
	for i := 0; i < tableSize; i++ {
		r.data[i] = &entry{key: i, val: int64(i)*3 + 1, name: "e"}
	}
	r.pool = &sync.Pool{New: func() any {
		atomic.AddInt32(&newCount, 1)
		return &scratch{buf: make([]byte, 32)}
	}}
}

func initWorker(id int, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	reg.once.Do(reg.init)
	e, ok := reg.data[tableSize-1]
	ck(ok && e != nil && e.key == tableSize-1 && e.val == int64(tableSize-1)*3+1 && e.name == "e",
		"initialized table entry invalid")
	ck(reg.pool != nil, "initialized pool is nil")
	if id == 0 {
		gcStress()
	}
}

func poolWorker(id int, start <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	<-start
	reg.once.Do(reg.init)
	for i := 0; i < poolRounds; i++ {
		s, _ := reg.pool.Get().(*scratch)
		if s == nil || s.used || s.p != nil || len(s.buf) != 32 || cap(s.buf) < 32 {
			ck(false, "pool returned dirty or malformed object")
			continue
		}
		s.used = true
		s.buf[0] = byte(i)
		s.p = &blob{tag: int64(id*1000 + i), name: "pool"}
		if i%32 == 0 {
			gcStress()
			if s.p == nil || s.p.tag != int64(id*1000+i) || s.p.name != "pool" {
				ck(false, "pooled payload corrupted by GC")
			}
			if s.buf[0] != byte(i) {
				ck(false, "pooled buffer corrupted by GC")
			}
			e := reg.data[(id+i)%tableSize]
			if e == nil || e.val != int64(e.key)*3+1 || e.name != "e" {
				ck(false, "shared table entry corrupted by GC")
			}
		}
		s.used = false
		s.p = nil
		s.buf = s.buf[:32]
		reg.pool.Put(s)
	}
}

func main() {
	start := make(chan struct{})
	var iw, pw sync.WaitGroup
	for i := 0; i < nInitGoroutines; i++ {
		iw.Add(1)
		go initWorker(i, start, &iw)
	}
	for i := 0; i < nPoolWorkers; i++ {
		pw.Add(1)
		go poolWorker(i, start, &pw)
	}
	close(start)
	iw.Wait()
	pw.Wait()

	ck(atomic.LoadInt32(&initCount) == 1, "sync.Once init ran more than once")
	gcStress()
	for k := 0; k < tableSize; k++ {
		e := reg.data[k]
		if e == nil || e.key != k || e.val != int64(k)*3+1 || e.name != "e" {
			ck(false, fmt.Sprintf("table entry %d corrupted after final GC", k))
			break
		}
	}
	// 池在新一轮中仍可用，且返回对象是干净的。
	s, _ := reg.pool.Get().(*scratch)
	if s == nil || s.used || s.p != nil || len(s.buf) != 32 {
		ck(false, "post-GC pool object is not clean")
	} else {
		reg.pool.Put(s)
	}

	fmt.Printf("once_inits=%d workers=%d acquires=%d\n",
		atomic.LoadInt32(&initCount), nPoolWorkers, nPoolWorkers*poolRounds)
	finish("l02_sync_once_pool_init")
}
