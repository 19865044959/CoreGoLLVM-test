// L01: 泛型 worker pool：固定 worker 从 channel 取任务、按 job id 回填结果，
// Close/Submit 状态机 + 跨 GC 校验任务负载指针与结果确定性。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type rec struct {
	id   int64
	name string
	blob []byte
}

type job[T any] struct {
	id int
	p  *T
}

type Pool[T any] struct {
	workers int
	jobs    chan job[T]
	wg      sync.WaitGroup
	closed  atomic.Bool
	fn      func(*T) int64
	results []int64
}

func NewPool[T any](workers, queue, njobs int, fn func(*T) int64) *Pool[T] {
	if workers < 1 {
		workers = 1
	}
	if queue < 1 {
		queue = 1
	}
	if njobs < 0 {
		njobs = 0
	}
	return &Pool[T]{
		workers: workers,
		jobs:    make(chan job[T], queue),
		fn:      fn,
		results: make([]int64, njobs),
	}
}

func (p *Pool[T]) Start() {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.loop()
	}
}

func (p *Pool[T]) loop() {
	defer p.wg.Done()
	for j := range p.jobs {
		p.results[j.id] = p.fn(j.p)
	}
}

func (p *Pool[T]) Submit(id int, v *T) bool {
	if p.closed.Load() {
		return false
	}
	p.jobs <- job[T]{id: id, p: v}
	return true
}

func (p *Pool[T]) Close() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.jobs)
	}
}

func (p *Pool[T]) Wait() { p.wg.Wait() }

func (p *Pool[T]) Result(i int) int64 { return p.results[i] }

var (
	failed  int32
	gcSink  [][]byte
	recSink []*rec
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 8 {
		n = 8
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(2)

	for round := 0; round < rounds; round++ {
		n := 120 + int(uafSeed%40)
		workers := 2 + int(uafSeed%3)
		cells := make([]*rec, n)
		for i := range cells {
			cells[i] = &rec{
				id:   int64(round*1000 + i),
				name: fmt.Sprintf("w%d_%d", round, i),
				blob: make([]byte, 6+i%8),
			}
			cells[i].blob[0] = byte(i)
			cells[i].blob[len(cells[i].blob)-1] = byte(round + 1)
		}

		pool := NewPool[rec](workers, 8, n, func(p *rec) int64 {
			return p.id*3 + 7
		})
		pool.Start()
		for i := 0; i < n; i++ {
			ck(pool.Submit(i, cells[i]), fmt.Sprintf("round=%d submit %d rejected while open", round, i))
			if i == n/2 {
				gcStress() // worker 正在消费时触发 GC
			}
		}
		gcStress()
		pool.Close()
		pool.Close() // 幂等
		ck(!pool.Submit(0, cells[0]), "submit after close was accepted")
		pool.Wait()

		for i := 0; i < n; i++ {
			ck(pool.Result(i) == cells[i].id*3+7, fmt.Sprintf("round=%d result %d corrupted", round, i))
			ck(cells[i].id == int64(round*1000+i), "job payload id corrupted")
			ck(cells[i].name == fmt.Sprintf("w%d_%d", round, i), "job payload name corrupted")
			ck(len(cells[i].blob) == 6+i%8 && cells[i].blob[0] == byte(i), "job payload blob corrupted")
		}
		recSink = append(recSink, cells...)
	}

	// 第二次实例化 Pool[int64]：值类型负载、不同 worker 数。
	nv := 64
	vals := make([]*int64, nv)
	for i := range vals {
		v := int64(i*5 + 2)
		vals[i] = &v
	}
	vp := NewPool[int64](3, 4, nv, func(p *int64) int64 { return *p*2 + 1 })
	vp.Start()
	for i := 0; i < nv; i++ {
		ck(vp.Submit(i, vals[i]), "int64 submit rejected while open")
	}
	vp.Close()
	vp.Wait()
	for i := 0; i < nv; i++ {
		ck(vp.Result(i) == *vals[i]*2+1, fmt.Sprintf("int64 result %d corrupted", i))
	}
	gcStress()
	for i := 0; i < nv; i++ {
		ck(*vals[i] == int64(i*5+2), "int64 payload corrupted after GC")
	}

	finish("l01_generic_worker_pool")
}
