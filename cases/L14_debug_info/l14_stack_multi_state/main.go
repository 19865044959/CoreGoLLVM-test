// L14: runtime.Stack(buf,true) 多状态 goroutine dump：chan receive / select /
// sleep / sync.Mutex.Lock 四类状态与对应函数符号必须齐全；唤醒后指针在 GC 后有效。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type stateNode struct {
	id   int64
	name string
}

var (
	failed    int32
	gcSink    [][]byte
	recvRe    = regexp.MustCompile(`goroutine \d+ \[chan receive\]:`)
	selectRe  = regexp.MustCompile(`goroutine \d+ \[select\]:`)
	sleepRe   = regexp.MustCompile(`goroutine \d+ \[sleep\]:`)
	mutexRe   = regexp.MustCompile(`goroutine \d+ \[sync\.Mutex\.Lock\]:`)
	lineRe    = regexp.MustCompile(`main\.go:\d+`)
	stateWait = 5 * time.Second
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func stateChanRecv(ch <-chan struct{}, ready chan<- struct{}, p *stateNode, errs *int32) {
	ready <- struct{}{}
	<-ch
	if p == nil || p.id != 1 || p.name != "recv" {
		atomic.AddInt32(errs, 1)
	}
}

//go:noinline
func stateSelect(a, b <-chan struct{}, ready chan<- struct{}, p *stateNode, errs *int32) {
	ready <- struct{}{}
	select {
	case <-a:
	case <-b:
	}
	if p == nil || p.id != 2 || p.name != "select" {
		atomic.AddInt32(errs, 1)
	}
}

//go:noinline
func stateSleep(ready chan<- struct{}, p *stateNode) {
	ready <- struct{}{}
	time.Sleep(time.Minute)
	_ = p.id
}

//go:noinline
func stateMutex(mu *sync.Mutex, ready chan<- struct{}, done chan<- struct{}, p *stateNode, errs *int32) {
	ready <- struct{}{}
	mu.Lock()
	mu.Unlock()
	if p == nil || p.id != 4 || p.name != "mutex" {
		atomic.AddInt32(errs, 1)
	}
	close(done)
}

func main() {
	ch := make(chan struct{})
	other := make(chan struct{})
	ready := make(chan struct{}, 4)
	mu := &sync.Mutex{}
	mu.Lock()
	var wg sync.WaitGroup
	mutexDone := make(chan struct{})
	errs := int32(0)

	wg.Add(1)
	go func() { defer wg.Done(); stateChanRecv(ch, ready, &stateNode{id: 1, name: "recv"}, &errs) }()
	wg.Add(1)
	go func() {
		defer wg.Done()
		stateSelect(ch, other, ready, &stateNode{id: 2, name: "select"}, &errs)
	}()
	go stateSleep(ready, &stateNode{id: 3, name: "sleep"})
	go stateMutex(mu, ready, mutexDone, &stateNode{id: 4, name: "mutex"}, &errs)

	for i := 0; i < 4; i++ {
		<-ready
	}
	gcStress()

	buf := make([]byte, 512<<10)
	deadline := time.Now().Add(stateWait)
	var dump string
	for {
		n := runtime.Stack(buf, true)
		dump = string(buf[:n])
		all := recvRe.MatchString(dump) && selectRe.MatchString(dump) &&
			sleepRe.MatchString(dump) && mutexRe.MatchString(dump)
		if all || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	ck(recvRe.MatchString(dump), "dump has chan receive state")
	ck(selectRe.MatchString(dump), "dump has select state")
	ck(sleepRe.MatchString(dump), "dump has sleep state")
	ck(mutexRe.MatchString(dump), "dump has mutex lock state")
	ck(dump != "" && lineRe.MatchString(dump), "dump has main.go:line")
	ck(containsSymbol(dump, "main.stateChanRecv"), "dump has stateChanRecv symbol")
	ck(containsSymbol(dump, "main.stateSelect"), "dump has stateSelect symbol")
	ck(containsSymbol(dump, "main.stateSleep"), "dump has stateSleep symbol")
	ck(containsSymbol(dump, "main.stateMutex"), "dump has stateMutex symbol")
	ck(containsSymbol(dump, "main.main"), "dump has main.main symbol")

	close(ch)
	wg.Wait()
	if mutexRe.MatchString(dump) {
		// 已确认该 goroutine 阻塞在 Mutex.Lock；此时 Unlock 不会命中未加锁状态。
		mu.Unlock()
		<-mutexDone
	}
	gcStress()
	ck(atomic.LoadInt32(&errs) == 0, "woken goroutine pointers after GC")
	finish("l14_stack_multi_state")
}

func containsSymbol(dump, fn string) bool {
	return len(dump) > 0 && strings.Contains(dump, fn)
}
