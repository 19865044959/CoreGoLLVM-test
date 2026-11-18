// L02: channel actor（etcd raft node / grpc callback serializer 场景）：
// 单 goroutine 独占状态，所有操作经 mailbox 串行化；按 key 校验命令 FIFO 顺序，
// 多生产者/读者并发投递，关闭 mailbox 后排空并交接最终状态。
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
	kindIncr = iota
	kindGet
	kindSnap
)

const (
	producers = 4
	perProd   = 50
	readers   = 2
	perReader = 30
)

type record struct {
	key int
	val int64
	tag string
}

type cmd struct {
	kind  int
	key   int
	seq   int
	delta int64
	pay   *record
	reply chan int64
}

type finalState struct {
	state map[int]*record
	last  map[int]int
	ops   int
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

func tagOf(key int) string { return fmt.Sprintf("rec-%d", key) }

func runActor(mb <-chan cmd, done chan struct{}, final chan finalState) {
	state := make(map[int]*record)
	last := make(map[int]int)
	ops := 0
	for c := range mb {
		switch c.kind {
		case kindIncr:
			rec := state[c.key]
			if rec == nil {
				rec = &record{key: c.key, tag: tagOf(c.key)}
				state[c.key] = rec
			}
			if c.seq != last[c.key]+1 {
				ck(false, "actor observed out-of-order command")
			}
			last[c.key] = c.seq
			rec.val += c.delta
			ops++
			if c.pay == nil || c.pay.key != c.key || c.pay.tag != "cmd" {
				ck(false, "command payload mismatch inside actor")
			}
			if ops%25 == 0 {
				gcStress()
				for _, r := range state {
					if r.tag != tagOf(r.key) {
						ck(false, "actor state corrupted by GC")
					}
				}
			}
		case kindGet:
			var v int64
			if rec := state[c.key]; rec != nil {
				v = rec.val
			}
			c.reply <- v
		case kindSnap:
			c.reply <- int64(len(state))
		default:
			ck(false, "unknown command kind")
		}
	}
	final <- finalState{state: state, last: last, ops: ops}
	close(done)
}

func main() {
	seed := seedVal()
	mb := make(chan cmd, 8)
	done := make(chan struct{})
	final := make(chan finalState, 1)
	go runActor(mb, done, final)

	var pwg sync.WaitGroup
	for p := 0; p < producers; p++ {
		pwg.Add(1)
		go func(p int) {
			defer pwg.Done()
			for i := 0; i < perProd; i++ {
				pay := &record{key: p, val: seed, tag: "cmd"}
				mb <- cmd{kind: kindIncr, key: p, seq: i + 1, delta: 1, pay: pay}
				if i%16 == 0 {
					gcStress()
					if pay.key != p || pay.tag != "cmd" {
						ck(false, "producer payload corrupted by GC")
					}
				}
			}
		}(p)
	}

	var rwg sync.WaitGroup
	for r := 0; r < readers; r++ {
		rwg.Add(1)
		go func(r int) {
			defer rwg.Done()
			prev := make([]int64, producers)
			for i := 0; i < perReader; i++ {
				key := (i + r) % producers
				reply := make(chan int64, 1)
				mb <- cmd{kind: kindGet, key: key, reply: reply}
				v := <-reply
				if v < prev[key] {
					ck(false, "actor value regressed for key")
				}
				if v < 0 || v > perProd {
					ck(false, fmt.Sprintf("actor value out of range: %d", v))
				}
				prev[key] = v
				if i%10 == 0 {
					gcStress()
				}
			}
		}(r)
	}

	pwg.Wait()
	rwg.Wait()
	snapReply := make(chan int64, 1)
	mb <- cmd{kind: kindSnap, reply: snapReply}
	if n := <-snapReply; n != producers {
		ck(false, fmt.Sprintf("snapshot keys=%d want=%d", n, producers))
	}
	close(mb)
	<-done
	fs := <-final

	ck(fs.ops == producers*perProd, fmt.Sprintf("actor ops=%d want=%d", fs.ops, producers*perProd))
	for k := 0; k < producers; k++ {
		rec := fs.state[k]
		if rec == nil {
			ck(false, fmt.Sprintf("missing record for key %d", k))
			continue
		}
		if rec.val != perProd {
			ck(false, fmt.Sprintf("key %d val=%d want=%d", k, rec.val, perProd))
		}
		if rec.tag != tagOf(k) {
			ck(false, fmt.Sprintf("key %d tag corrupted", k))
		}
		if fs.last[k] != perProd {
			ck(false, fmt.Sprintf("key %d last seq=%d want=%d", k, fs.last[k], perProd))
		}
	}
	gcStress()
	for k := 0; k < producers; k++ {
		rec := fs.state[k]
		if rec == nil || rec.val != perProd || rec.tag != tagOf(k) {
			ck(false, "final state corrupted by GC")
		}
	}

	fmt.Printf("producers=%d ops=%d reads=%d keys=%d\n",
		producers, producers*perProd, readers*perReader, producers)
	finish("l02_actor_mailbox_state")
}
