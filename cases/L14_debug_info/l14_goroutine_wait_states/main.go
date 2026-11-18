// L14: 多 goroutine 状态 dump（新状态组合）。同一轮内放置五种阻塞状态：
// sync.RWMutex.RLock、sync.RWMutex.Lock、nil channel send、nil channel receive、
// 无 case 的 select。runtime.Stack(buf,true) 必须给出完整状态头、真实阻塞函数符号
// 与 main.go:行号；阻塞期间持有的指针在 GC 后仍有效。TORTURE_ROUNDS 放大轮数。
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

type wsPayload struct {
	id   int64
	name string
}

const (
	wsRLock   = int64(1)
	wsWLock   = int64(2)
	wsNilSend = int64(3)
	wsNilRecv = int64(4)
	wsNoCase  = int64(5)
)

var (
	failed   int32
	gcSink   [][]byte
	rlockRe  = regexp.MustCompile(`goroutine \d+ \[sync\.RWMutex\.RLock\]:`)
	wlockRe  = regexp.MustCompile(`goroutine \d+ \[sync\.RWMutex\.Lock\]:`)
	nilSndRe = regexp.MustCompile(`goroutine \d+ \[chan send \(nil chan\)\]:`)
	nilRcvRe = regexp.MustCompile(`goroutine \d+ \[chan receive \(nil chan\)\]:`)
	noCaseRe = regexp.MustCompile(`goroutine \d+ \[select \(no cases\)\]:`)
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

func wsCheck(p *wsPayload, id int64, name string, errs *int32) {
	if p == nil || p.id != id || p.name != name {
		atomic.AddInt32(errs, 1)
	}
}

//go:noinline
func waitRLock(mu *sync.RWMutex, ready chan<- struct{}, done chan<- struct{}, p *wsPayload, errs *int32) {
	ready <- struct{}{}
	mu.RLock()
	wsCheck(p, wsRLock, "rlock", errs)
	mu.RUnlock()
	close(done)
}

//go:noinline
func waitWLock(mu *sync.RWMutex, ready chan<- struct{}, done chan<- struct{}, p *wsPayload, errs *int32) {
	ready <- struct{}{}
	mu.Lock()
	wsCheck(p, wsWLock, "wlock", errs)
	mu.Unlock()
	close(done)
}

//go:noinline
func waitNilSend(ch chan struct{}, ready chan<- struct{}, p *wsPayload) {
	ready <- struct{}{}
	ch <- struct{}{} // nil channel：永久阻塞在 chan send (nil chan)
	_ = p.id
}

//go:noinline
func waitNilRecv(ch chan struct{}, ready chan<- struct{}, p *wsPayload) {
	ready <- struct{}{}
	<-ch // nil channel：永久阻塞在 chan receive (nil chan)
	_ = p.id
}

//go:noinline
func waitNoCase(ready chan<- struct{}, p *wsPayload) {
	ready <- struct{}{}
	select {} // 永久阻塞在 select (no cases)
}

func runRound(round int) {
	// RLock 阻塞：主 goroutine 先持有写锁。
	rwA := &sync.RWMutex{}
	rwA.Lock()
	// Lock 阻塞：主 goroutine 先持有读锁。
	rwB := &sync.RWMutex{}
	rwB.RLock()

	var nilSend chan struct{}
	var nilRecv chan struct{}

	ready := make(chan struct{}, 5)
	doneR := make(chan struct{})
	doneW := make(chan struct{})
	errs := int32(0)

	pR := &wsPayload{id: wsRLock, name: "rlock"}
	pW := &wsPayload{id: wsWLock, name: "wlock"}
	pS := &wsPayload{id: wsNilSend, name: "nil-send"}
	pV := &wsPayload{id: wsNilRecv, name: "nil-recv"}
	pN := &wsPayload{id: wsNoCase, name: "no-case"}

	go waitRLock(rwA, ready, doneR, pR, &errs)
	go waitWLock(rwB, ready, doneW, pW, &errs)
	go waitNilSend(nilSend, ready, pS)
	go waitNilRecv(nilRecv, ready, pV)
	go waitNoCase(ready, pN)

	for i := 0; i < 5; i++ {
		<-ready
	}
	gcStress()

	buf := make([]byte, 1<<20)
	deadline := time.Now().Add(3 * time.Second)
	var dump string
	for {
		n := runtime.Stack(buf, true)
		dump = string(buf[:n])
		if rlockRe.MatchString(dump) && wlockRe.MatchString(dump) &&
			nilSndRe.MatchString(dump) && nilRcvRe.MatchString(dump) &&
			noCaseRe.MatchString(dump) {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	prefix := "round " + strconv.Itoa(round) + ": "
	ck(rlockRe.MatchString(dump), prefix+"dump has sync.RWMutex.RLock state")
	ck(wlockRe.MatchString(dump), prefix+"dump has sync.RWMutex.Lock state")
	ck(nilSndRe.MatchString(dump), prefix+"dump has nil chan send state")
	ck(nilRcvRe.MatchString(dump), prefix+"dump has nil chan receive state")
	ck(noCaseRe.MatchString(dump), prefix+"dump has select no cases state")
	ck(strings.Contains(dump, "main.waitRLock("), prefix+"dump has waitRLock symbol")
	ck(strings.Contains(dump, "main.waitWLock("), prefix+"dump has waitWLock symbol")
	ck(strings.Contains(dump, "main.waitNilSend("), prefix+"dump has waitNilSend symbol")
	ck(strings.Contains(dump, "main.waitNilRecv("), prefix+"dump has waitNilRecv symbol")
	ck(strings.Contains(dump, "main.waitNoCase("), prefix+"dump has waitNoCase symbol")
	ck(strings.Contains(dump, "main.main("), prefix+"dump has main.main symbol")
	ck(lineRe.MatchString(dump), prefix+"dump has main.go:line")

	// 释放两个 RWMutex 阻塞者；nil chan / no-case goroutine 保持阻塞到进程结束。
	rwA.Unlock()
	<-doneR
	rwB.RUnlock()
	<-doneW
	gcStress()
	ck(atomic.LoadInt32(&errs) == 0, prefix+"blocked RWMutex goroutine pointers after GC")
	ck(pR.id == wsRLock && pR.name == "rlock", prefix+"rlock payload after GC")
	ck(pW.id == wsWLock && pW.name == "wlock", prefix+"wlock payload after GC")
	ck(pS.id == wsNilSend && pS.name == "nil-send", prefix+"nil send payload after GC")
	ck(pV.id == wsNilRecv && pV.name == "nil-recv", prefix+"nil receive payload after GC")
	ck(pN.id == wsNoCase && pN.name == "no-case", prefix+"no-case payload after GC")
}

func main() {
	rounds := tortureRounds(1)
	for r := 1; r <= rounds; r++ {
		runRound(r)
	}
	finish("l14_goroutine_wait_states")
}
