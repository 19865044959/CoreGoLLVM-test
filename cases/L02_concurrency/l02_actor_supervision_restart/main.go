// L02: actor 监督与重启（etcd raft 单 goroutine 事件循环 / grpc 关闭排空场景）：
// 子 actor 处理命令时 panic，监督者 recover、重建子状态并继续；
// 重启预算耗尽后升级为 stopped，后续命令被丢弃但仍返回响应，不阻塞发送者。
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
	svMaxRestarts = 2
	svNormal1     = 5
	svNormal2     = 3
	svNormal3     = 2
	svAfterStop   = 2
)

const (
	kindIncr = iota
	kindPoison
)

type svRecord struct {
	key int
	val int64
	tag string
}

type svChild struct {
	gen     int
	counter int64
	records map[int]*svRecord
}

func newChild(gen int) *svChild {
	return &svChild{gen: gen, records: make(map[int]*svRecord)}
}

type svCmd struct {
	kind  int
	key   int
	delta int64
	pay   *svRecord
	reply chan svReply
}

type svReply struct {
	status   string
	counter  int64
	restarts int
	gen      int
	echo     *svRecord
}

type supervisor struct {
	mb       <-chan svCmd
	done     chan struct{}
	child    *svChild
	restarts int
	stopped  bool
	handled  int
	dropped  int
}

func newSupervisor(mb <-chan svCmd) *supervisor {
	return &supervisor{mb: mb, done: make(chan struct{}), child: newChild(0)}
}

// apply 在 recover 保护下修改子状态；panic 表示子 actor 崩溃。
func (s *supervisor) apply(c *svCmd) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
		}
	}()
	switch c.kind {
	case kindIncr:
		if c.pay == nil || c.pay.tag != "inc" {
			panic("bad command payload")
		}
		rec := s.child.records[c.key]
		if rec == nil {
			rec = &svRecord{key: c.key, tag: fmt.Sprintf("r%d", c.key)}
			s.child.records[c.key] = rec
		}
		rec.val += c.delta
		s.child.counter += c.delta
	case kindPoison:
		panic("poison command")
	default:
		panic("unknown command")
	}
	return false
}

func (s *supervisor) run() {
	defer close(s.done)
	for c := range s.mb {
		if s.stopped {
			s.dropped++
			c.reply <- svReply{status: "stopped", counter: s.child.counter, restarts: s.restarts, gen: s.child.gen}
			continue
		}
		panicked := s.apply(&c)
		if panicked {
			if s.restarts >= svMaxRestarts {
				s.stopped = true
				c.reply <- svReply{status: "stopped", counter: s.child.counter, restarts: s.restarts, gen: s.child.gen}
				continue
			}
			s.restarts++
			s.child = newChild(s.restarts) // 丢弃崩溃前的部分状态
			c.reply <- svReply{status: "restart", counter: 0, restarts: s.restarts, gen: s.child.gen}
			continue
		}
		s.handled++
		if s.handled%4 == 0 {
			gcStress()
			for k, r := range s.child.records {
				if r == nil || r.key != k || r.tag != fmt.Sprintf("r%d", k) {
					ck(false, fmt.Sprintf("supervisor state corrupted by GC key=%d", k))
				}
			}
		}
		c.reply <- svReply{status: "ok", counter: s.child.counter, restarts: s.restarts, gen: s.child.gen, echo: c.pay}
	}
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

type producer struct {
	mb   chan<- svCmd
	seed int64
	seq  int
}

func (p *producer) send(c svCmd) svReply {
	c.reply = make(chan svReply, 1)
	p.mb <- c
	r := <-c.reply
	p.seq++
	if p.seq%3 == 0 {
		gcStress()
	}
	return r
}

func (p *producer) incr(key int) svReply {
	pay := &svRecord{key: key, val: p.seed + int64(p.seq), tag: "inc"}
	r := p.send(svCmd{kind: kindIncr, key: key, delta: 1, pay: pay})
	if r.status == "ok" {
		if r.echo == nil || r.echo.key != key || r.echo.tag != "inc" {
			ck(false, "command payload corrupted across mailbox/GC")
		}
	}
	return r
}

func (p *producer) poison() svReply {
	return p.send(svCmd{kind: kindPoison, key: -1})
}

func main() {
	seed := seedVal()
	mb := make(chan svCmd)
	s := newSupervisor(mb)
	go s.run()
	p := &producer{mb: mb, seed: seed}

	// 第一阶段：5 个正常命令。
	for i := 0; i < svNormal1; i++ {
		r := p.incr(0)
		if r.status != "ok" || r.counter != int64(i+1) || r.gen != 0 {
			ck(false, fmt.Sprintf("phase1 reply=%+v want counter=%d gen=0", r, i+1))
		}
	}
	// 第一次 poison：重启，计数器归零。
	r := p.poison()
	if r.status != "restart" || r.counter != 0 || r.restarts != 1 || r.gen != 1 {
		ck(false, fmt.Sprintf("first restart reply=%+v", r))
	}
	for i := 0; i < svNormal2; i++ {
		r = p.incr(1)
		if r.status != "ok" || r.counter != int64(i+1) || r.gen != 1 || r.restarts != 1 {
			ck(false, fmt.Sprintf("phase2 reply=%+v want counter=%d gen=1", r, i+1))
		}
	}
	// 第二次 poison：再次重启。
	r = p.poison()
	if r.status != "restart" || r.counter != 0 || r.restarts != 2 || r.gen != 2 {
		ck(false, fmt.Sprintf("second restart reply=%+v", r))
	}
	for i := 0; i < svNormal3; i++ {
		r = p.incr(2)
		if r.status != "ok" || r.counter != int64(i+1) || r.gen != 2 || r.restarts != 2 {
			ck(false, fmt.Sprintf("phase3 reply=%+v want counter=%d gen=2", r, i+1))
		}
	}
	// 第三次 poison：预算耗尽，升级为 stopped。
	r = p.poison()
	if r.status != "stopped" || r.restarts != 2 {
		ck(false, fmt.Sprintf("escalation reply=%+v", r))
	}
	// stopped 后命令仍被响应（dropped），发送者不会阻塞。
	for i := 0; i < svAfterStop; i++ {
		r = p.incr(3)
		if r.status != "stopped" {
			ck(false, fmt.Sprintf("post-stop reply=%+v want stopped", r))
		}
	}
	close(mb)
	<-s.done

	if s.handled != svNormal1+svNormal2+svNormal3 {
		ck(false, fmt.Sprintf("handled=%d want=%d", s.handled, svNormal1+svNormal2+svNormal3))
	}
	if s.dropped != svAfterStop {
		ck(false, fmt.Sprintf("dropped=%d want=%d", s.dropped, svAfterStop))
	}
	if s.restarts != svMaxRestarts {
		ck(false, fmt.Sprintf("restarts=%d want=%d", s.restarts, svMaxRestarts))
	}
	if !s.stopped {
		ck(false, "supervisor must be stopped after restart budget exhausted")
	}
	gcStress()
	if s.child == nil || s.child.counter != int64(svNormal3) {
		ck(false, "final child state inconsistent")
	}

	commands := svNormal1 + svNormal2 + svNormal3 + 3 + svAfterStop
	fmt.Printf("commands=%d handled=%d dropped=%d restarts=%d\n",
		commands, s.handled, s.dropped, s.restarts)
	finish("l02_actor_supervision_restart")
}
