// L02: fan-out + fan-in 组合：dispatcher 广播事件到 3 个容量 1 的订阅信箱，
// 每个订阅按序处理后向共享 ack channel fan-in；其中一个订阅中途暂停制造
// 背压。校验每订阅全量、有序、恰好一次，ack 总数 = 事件数 × 订阅数。
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
	fSubs       = 3
	fEvents     = 64
	fInboxCap   = 1
	fPauseAfter = fEvents / 2
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
)

type fevent struct {
	seq int
	v   int64
	tag int
}

type fack struct {
	sub int
	seq int
	v   int64
	chk int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

func fVal(seed int64, seq int) int64 { return seed*401 + int64(seq)*29 + 11 }
func fChk(v int64) int64             { return v*3 + 17 }

func main() {
	curSeed = envSeed()

	inboxes := make([]chan *fevent, fSubs)
	for i := range inboxes {
		inboxes[i] = make(chan *fevent, fInboxCap)
	}
	acks := make(chan fack, fSubs*fEvents)
	paused := make(chan struct{})
	resume := make(chan struct{})

	var subWG sync.WaitGroup
	for s := 0; s < fSubs; s++ {
		subWG.Add(1)
		go func(s int) {
			defer subWG.Done()
			want := 0
			n := 0
			for ev := range inboxes[s] {
				if ev == nil || ev.seq != want || ev.v != fVal(curSeed, ev.seq) || ev.tag != s {
					ckSeed(false, fmt.Sprintf("subscriber %d received out-of-order or corrupted event", s))
					continue
				}
				want++
				n++
				if n%16 == 0 {
					gcStress()
					if ev.v != fVal(curSeed, ev.seq) {
						ckSeed(false, "event corrupted after GC")
					}
				}
				if s == 0 && n == fPauseAfter {
					close(paused)
					<-resume
				}
				acks <- fack{sub: s, seq: ev.seq, v: ev.v, chk: fChk(ev.v)}
			}
			if n != fEvents {
				ckSeed(false, fmt.Sprintf("subscriber %d got=%d want=%d", s, n, fEvents))
			}
		}(s)
	}

	go func() {
		subWG.Wait()
		close(acks)
	}()

	go func() {
		for seq := 0; seq < fEvents; seq++ {
			if seq%13 == 0 {
				gcStress()
			}
			for s := 0; s < fSubs; s++ {
				ev := &fevent{seq: seq, v: fVal(curSeed, seq), tag: s}
				if ev.v != fVal(curSeed, seq) || ev.tag != s {
					ckSeed(false, "dispatcher event corrupted before send")
				}
				inboxes[s] <- ev
			}
		}
		for s := 0; s < fSubs; s++ {
			close(inboxes[s])
		}
	}()

	<-paused
	// 此时其他订阅者仍在消费，dispatcher 可能被慢订阅者反压；恢复后必须全量收敛。
	gcStress()
	close(resume)

	count := 0
	perSub := make([]int, fSubs)
	last := make([]int, fSubs)
	for i := range last {
		last[i] = -1
	}
	for a := range acks {
		if a.sub < 0 || a.sub >= fSubs {
			ck(false, "ack with invalid subscriber id")
			continue
		}
		if a.seq != last[a.sub]+1 {
			ckSeed(false, fmt.Sprintf("ack order broken sub=%d seq=%d last=%d", a.sub, a.seq, last[a.sub]))
		}
		last[a.sub] = a.seq
		if a.v != fVal(curSeed, a.seq) || a.chk != fChk(a.v) {
			ckSeed(false, "ack payload corrupted")
		}
		perSub[a.sub]++
		count++
		if count%32 == 0 {
			gcStress()
			if a.v != fVal(curSeed, a.seq) {
				ckSeed(false, "ack corrupted after GC")
			}
		}
	}
	subWG.Wait()
	for s := 0; s < fSubs; s++ {
		if perSub[s] != fEvents {
			ckSeed(false, fmt.Sprintf("sub %d acks=%d want=%d", s, perSub[s], fEvents))
		}
	}
	if count != fSubs*fEvents {
		ckSeed(false, fmt.Sprintf("acks=%d want=%d", count, fSubs*fEvents))
	}

	fmt.Printf("subscribers=%d events=%d acks=%d\n", fSubs, fEvents, count)
	finish("l02_broadcast_fanin_acks")
}
