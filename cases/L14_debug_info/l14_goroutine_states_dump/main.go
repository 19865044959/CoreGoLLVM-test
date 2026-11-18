// L14: 多 goroutine 状态 dump。四类阻塞状态 chan send / select /
// sync.WaitGroup.Wait / sync.Cond.Wait 的 goroutine 头与函数符号必须齐全，
// 且阻塞期间指针在 GC 后有效。TORTURE_ROUNDS 放大 dump 轮数。
package main

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type stateNode struct {
	id   int64
	name string
}

const (
	stateSend = int64(1)
	stateSel  = int64(2)
	stateWG   = int64(3)
	stateCond = int64(4)
)

var (
	failed   int32
	gcSink   [][]byte
	sendRe   = regexp.MustCompile(`goroutine \d+ \[chan send\]:`)
	selectRe = regexp.MustCompile(`goroutine \d+ \[select\]:`)
	wgRe     = regexp.MustCompile(`goroutine \d+ \[sync\.WaitGroup\.Wait\]:`)
	condRe   = regexp.MustCompile(`goroutine \d+ \[sync\.Cond\.Wait\]:`)
	lineRe   = regexp.MustCompile(`main\.go:\d+`)
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

func checkNode(p *stateNode, id int64, name string, errs *int32) {
	if p == nil || p.id != id || p.name != name {
		atomic.AddInt32(errs, 1)
	}
}

//go:noinline
func stateChanSend(ch chan struct{}, ready chan<- struct{}, done chan<- struct{}, p *stateNode, errs *int32) {
	ready <- struct{}{}
	ch <- struct{}{}
	checkNode(p, stateSend, "send", errs)
	<-ch
	close(done)
}

//go:noinline
func stateSelect(a, b <-chan struct{}, ready chan<- struct{}, done chan<- struct{}, p *stateNode, errs *int32) {
	ready <- struct{}{}
	select {
	case <-a:
	case <-b:
	}
	checkNode(p, stateSel, "select", errs)
	close(done)
}

//go:noinline
func stateWGWait(wg *sync.WaitGroup, ready chan<- struct{}, done chan<- struct{}, p *stateNode, errs *int32) {
	ready <- struct{}{}
	wg.Wait()
	checkNode(p, stateWG, "waitgroup", errs)
	close(done)
}

//go:noinline
func stateCondWait(cond *sync.Cond, ready chan<- struct{}, done chan<- struct{}, p *stateNode, errs *int32) {
	cond.L.Lock()
	ready <- struct{}{}
	cond.Wait()
	cond.L.Unlock()
	checkNode(p, stateCond, "cond", errs)
	close(done)
}

func runRound(round int) {
	ch := make(chan struct{})
	selA := make(chan struct{})
	selB := make(chan struct{})
	ready := make(chan struct{}, 4)
	doneSend := make(chan struct{})
	doneSel := make(chan struct{})
	doneWG := make(chan struct{})
	doneCond := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1) // 故意不 Done，制造 sync.WaitGroup.Wait
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	errs := int32(0)

	sendNode := &stateNode{id: stateSend, name: "send"}
	selNode := &stateNode{id: stateSel, name: "select"}
	wgNode := &stateNode{id: stateWG, name: "waitgroup"}
	condNode := &stateNode{id: stateCond, name: "cond"}

	go stateChanSend(ch, ready, doneSend, sendNode, &errs)
	go stateSelect(selA, selB, ready, doneSel, selNode, &errs)
	go stateWGWait(&wg, ready, doneWG, wgNode, &errs)
	go stateCondWait(cond, ready, doneCond, condNode, &errs)

	for i := 0; i < 4; i++ {
		<-ready
	}
	gcStress()

	buf := make([]byte, 512<<10)
	deadline := time.Now().Add(3 * time.Second)
	var dump string
	for {
		n := runtime.Stack(buf, true)
		dump = string(buf[:n])
		if sendRe.MatchString(dump) && selectRe.MatchString(dump) &&
			wgRe.MatchString(dump) && condRe.MatchString(dump) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	prefix := "round " + strconv.Itoa(round) + ": "
	ck(sendRe.MatchString(dump), prefix+"dump has chan send state")
	ck(selectRe.MatchString(dump), prefix+"dump has select state")
	ck(wgRe.MatchString(dump), prefix+"dump has WaitGroup.Wait state")
	ck(condRe.MatchString(dump), prefix+"dump has Cond.Wait state")
	ck(strings.Contains(dump, "main.stateChanSend("), prefix+"dump has stateChanSend symbol")
	ck(strings.Contains(dump, "main.stateSelect("), prefix+"dump has stateSelect symbol")
	ck(strings.Contains(dump, "main.stateWGWait("), prefix+"dump has stateWGWait symbol")
	ck(strings.Contains(dump, "main.stateCondWait("), prefix+"dump has stateCondWait symbol")
	ck(strings.Contains(dump, "main.main("), prefix+"dump has main.main symbol")
	ck(lineRe.MatchString(dump), prefix+"dump has main.go:line")

	// 放行四种状态，校验阻塞期间持有的指针。
	<-ch
	close(ch)
	close(selA)
	wg.Done()
	mu.Lock()
	cond.Signal()
	mu.Unlock()

	<-doneSend
	<-doneSel
	<-doneWG
	<-doneCond
	gcStress()
	ck(atomic.LoadInt32(&errs) == 0, prefix+"blocked goroutine pointers after GC")
	ck(sendNode.id == stateSend && selNode.id == stateSel &&
		wgNode.id == stateWG && condNode.id == stateCond, prefix+"state payloads after GC")
}

func main() {
	rounds := tortureRounds(1)
	for r := 1; r <= rounds; r++ {
		runRound(r)
	}
	finish("l14_goroutine_states_dump")
}
