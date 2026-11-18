// L02: credits 背压（grpc 流控窗口 / kitex writeQuota 场景）：
// 发送方每发一条消息消耗一个 credit；窗口耗尽后阻塞等待接收方批量归还。
// needCredit 标志把“发送方已经阻塞”这一时刻确定化，保证 blocked 计数可复现。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	crWindow    = 4
	crBaseTotal = 64
)

type crMsg struct {
	seq int
	val int64
	tag string
}

type crStats struct {
	sent     int
	received int
	returned int
	blocked  int
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

func tortureScale() int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 1 {
			if n > 16 {
				n = 16
			}
			return n
		}
	}
	return 1
}

func main() {
	seed := seedVal()
	scale := tortureScale()
	total := crBaseTotal * scale
	data := make(chan *crMsg)
	credits := make(chan int, crWindow)
	for i := 0; i < crWindow; i++ {
		credits <- 1
	}
	var needCredit int32
	var stats crStats
	var swg, rwg sync.WaitGroup

	// 接收方：严格按序消费；每消费一个窗口就归还同量 credit。
	rwg.Add(1)
	go func() {
		defer rwg.Done()
		expected := 0
		inBatch := 0
		for m := range data {
			if m == nil {
				ck(false, "nil message on data channel")
				continue
			}
			if m.seq != expected {
				ck(false, fmt.Sprintf("receiver seq=%d want=%d", m.seq, expected))
			}
			if m.val != seed+int64(m.seq)*11 || m.tag != "m" {
				ck(false, fmt.Sprintf("message payload corrupted seq=%d", m.seq))
			}
			expected++
			stats.received++
			inBatch++
			if m.seq%8 == 0 {
				gcStress()
				if m.val != seed+int64(m.seq)*11 || m.tag != "m" {
					ck(false, fmt.Sprintf("message payload corrupted after GC seq=%d", m.seq))
				}
			}
			if inBatch == crWindow {
				isFinal := stats.received == total
				if !isFinal {
					// 等待发送方真正因窗口耗尽而阻塞，避免依赖墙钟。
					for atomic.LoadInt32(&needCredit) == 0 {
						runtime.Gosched()
					}
				}
				for i := 0; i < crWindow; i++ {
					credits <- 1
				}
				stats.returned += crWindow
				inBatch = 0
			}
		}
		if expected != total {
			ck(false, fmt.Sprintf("receiver expected=%d want=%d", expected, total))
		}
	}()

	// 发送方：acquire 一个 credit 才能发送；每窗口末尾必定阻塞等待归还。
	swg.Add(1)
	go func() {
		defer swg.Done()
		for seq := 0; seq < total; seq++ {
			if seq > 0 && seq%crWindow == 0 {
				atomic.StoreInt32(&needCredit, 1)
				stats.blocked++
				<-credits
				atomic.StoreInt32(&needCredit, 0)
			} else {
				<-credits
			}
			m := &crMsg{seq: seq, val: seed + int64(seq)*11, tag: "m"}
			data <- m
			stats.sent++
			if seq%8 == 0 {
				gcStress()
			}
		}
		close(data)
	}()

	swg.Wait()
	rwg.Wait()

	if stats.sent != total {
		ck(false, fmt.Sprintf("sent=%d want=%d", stats.sent, total))
	}
	if stats.received != total {
		ck(false, fmt.Sprintf("received=%d want=%d", stats.received, total))
	}
	if stats.returned != total {
		ck(false, fmt.Sprintf("returned=%d want=%d", stats.returned, total))
	}
	wantBlocked := total/crWindow - 1
	if stats.blocked != wantBlocked {
		ck(false, fmt.Sprintf("blocked=%d want=%d", stats.blocked, wantBlocked))
	}
	if n := len(credits); n != crWindow {
		ck(false, fmt.Sprintf("residual credits=%d want=%d", n, crWindow))
	}
	if n := cap(credits) - len(credits); n != 0 {
		ck(false, fmt.Sprintf("outstanding credits=%d want=0", n))
	}

	gcStress()
	fmt.Printf("window=%d total=%d returned=%d blocked=%d\n",
		crWindow, total, stats.returned, stats.blocked)
	finish("l02_flow_credits_backpressure")
}
