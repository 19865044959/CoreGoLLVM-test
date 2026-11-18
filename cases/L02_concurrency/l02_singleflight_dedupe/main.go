// L02: singleflight 类去重（标准库实现）：同一 key 的并发 Do 只执行一次 fn，
// 其余调用等待并共享结果；不同 key 各自合并；错误不被缓存，完成后可重算。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var errExec = errors.New("exec failed")

type payload struct {
	v   int64
	tag string
}

type call struct {
	wg      sync.WaitGroup
	val     *payload
	err     error
	waiters int
}

type flight struct {
	mu sync.Mutex
	m  map[string]*call
}

func newFlight() *flight { return &flight{m: make(map[string]*call)} }

func (f *flight) Do(key string, fn func() (*payload, error)) (*payload, error, bool) {
	f.mu.Lock()
	if c, ok := f.m[key]; ok {
		c.waiters++
		f.mu.Unlock()
		c.wg.Wait()
		return c.val, c.err, true
	}
	c := &call{}
	c.wg.Add(1)
	f.m[key] = c
	f.mu.Unlock()

	val, err := fn()

	f.mu.Lock()
	delete(f.m, key)
	f.mu.Unlock()
	c.val = val
	c.err = err
	c.wg.Done()
	return val, err, false
}

func (f *flight) totalDups() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.m {
		n += c.waiters
	}
	return n
}

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

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

func seedVal() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return int64(n)
		}
	}
	return 1
}

func waitFor(cond func() bool, msg string) {
	for i := 0; i < 4000000; i++ {
		if cond() {
			return
		}
		runtime.Gosched()
	}
	ck(false, msg)
}

func main() {
	seed := seedVal()
	f := newFlight()

	// 阶段一：8 个并发调用合并为 1 次执行。
	var computes int32
	var sharedFalse int32
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	results := make([]*payload, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err, shared := f.Do("alpha", func() (*payload, error) {
				atomic.AddInt32(&computes, 1)
				entered <- struct{}{}
				<-release
				p := &payload{v: seed + 7, tag: "alpha"}
				gcStress()
				if p.v != seed+7 || p.tag != "alpha" {
					ck(false, "computed payload corrupted by GC")
				}
				return p, nil
			})
			if err != nil {
				ck(false, "alpha Do returned error")
				return
			}
			if !shared {
				atomic.AddInt32(&sharedFalse, 1)
			}
			results[idx] = v
		}(i)
	}
	<-entered
	waitFor(func() bool { return f.totalDups() == 7 }, "not all alpha followers enrolled")
	close(release)
	wg.Wait()
	ck(atomic.LoadInt32(&computes) == 1, fmt.Sprintf("alpha computes=%d want=1", computes))
	ck(atomic.LoadInt32(&sharedFalse) == 1, fmt.Sprintf("alpha leaders=%d want=1", sharedFalse))
	first := results[0]
	for i, p := range results {
		if p == nil || p != first || p.v != seed+7 || p.tag != "alpha" {
			ck(false, fmt.Sprintf("alpha result %d inconsistent", i))
		}
	}
	gcStress()
	if first == nil || first.v != seed+7 || first.tag != "alpha" {
		ck(false, "alpha result corrupted after GC")
	}

	// 阶段二：不同 key 各自合并，共 3 次执行。
	keys := []string{"k0", "k1", "k2"}
	computeByKey := make([]int32, 3)
	gates := make([]chan struct{}, 3)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	enteredKey := make(chan struct{}, 3)
	var kwg sync.WaitGroup
	for j := 0; j < 3; j++ {
		for i := 0; i < 3; i++ {
			kwg.Add(1)
			go func(j int) {
				defer kwg.Done()
				v, err, _ := f.Do(keys[j], func() (*payload, error) {
					atomic.AddInt32(&computeByKey[j], 1)
					enteredKey <- struct{}{}
					<-gates[j]
					return &payload{v: seed + int64(j), tag: keys[j]}, nil
				})
				if err != nil || v == nil || v.v != seed+int64(j) || v.tag != keys[j] {
					ck(false, "distinct-key result mismatch")
				}
			}(j)
		}
	}
	for i := 0; i < 3; i++ {
		<-enteredKey
	}
	waitFor(func() bool { return f.totalDups() == 6 }, "distinct-key followers not enrolled")
	for j := range gates {
		close(gates[j])
	}
	kwg.Wait()
	for j := 0; j < 3; j++ {
		ck(atomic.LoadInt32(&computeByKey[j]) == 1, fmt.Sprintf("key %s computes=%d want=1", keys[j], computeByKey[j]))
	}

	// 阶段三：错误共享但不缓存，结束后重新执行。
	var errComputes int32
	errEntered := make(chan struct{}, 1)
	errRelease := make(chan struct{})
	errResults := make([]error, 4)
	var ewg sync.WaitGroup
	for i := 0; i < 4; i++ {
		ewg.Add(1)
		go func(idx int) {
			defer ewg.Done()
			_, err, _ := f.Do("err", func() (*payload, error) {
				atomic.AddInt32(&errComputes, 1)
				errEntered <- struct{}{}
				<-errRelease
				return nil, errExec
			})
			errResults[idx] = err
		}(i)
	}
	<-errEntered
	waitFor(func() bool { return f.totalDups() == 3 }, "error followers not enrolled")
	close(errRelease)
	ewg.Wait()
	for i, err := range errResults {
		if !errors.Is(err, errExec) {
			ck(false, fmt.Sprintf("error result %d=%v want exec failed", i, err))
		}
	}
	ck(atomic.LoadInt32(&errComputes) == 1, fmt.Sprintf("err computes=%d want=1", errComputes))

	v, err, shared := f.Do("err", func() (*payload, error) {
		atomic.AddInt32(&errComputes, 1)
		return &payload{v: seed + 99, tag: "retry"}, nil
	})
	ck(err == nil && !shared, "single sequential call must not be shared")
	ck(v != nil && v.v == seed+99 && v.tag == "retry", "retry payload mismatch")
	ck(atomic.LoadInt32(&errComputes) == 2, fmt.Sprintf("err computes after retry=%d want=2", errComputes))

	fmt.Printf("alpha_computes=1 alpha_dups=7 distinct_computes=3 err_computes=2\n")
	finish("l02_singleflight_dedupe")
}
