// L02: context cause 链（Go context TestCause / grpc 取消竞态场景）：
// WithCancelCause / WithDeadlineCause / WithTimeoutCause 的 Cause 传播，
// 首个 cause 获胜、父子/兄弟隔离、nil cause 回退到 ctx.Err。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

var (
	errParent   = errors.New("parent-cause")
	errChild    = errors.New("child-cause")
	errDeadline = errors.New("deadline-cause")
	errTimeout  = errors.New("timeout-cause")
)

type cwPayload struct {
	id  int
	val int64
	tag string
}

func (p *cwPayload) valid(seed int64) bool {
	return p != nil && p.val == seed+int64(p.id)*23 && p.tag == fmt.Sprintf("w%d", p.id)
}

const cwWaiters = 6

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

func main() {
	seed := seedVal()

	// 无 cause 的 Background。
	if c := context.Cause(context.Background()); c != nil {
		ck(false, fmt.Sprintf("Background cause=%v want=nil", c))
	}

	parent, cancelParent := context.WithCancelCause(context.Background())
	child, cancelChild := context.WithCancelCause(parent)
	sibling, cancelSibling := context.WithCancelCause(parent)
	grand, cancelGrand := context.WithCancelCause(child)
	defer cancelSibling(nil)
	defer cancelGrand(nil)

	waiting := make(chan int, cwWaiters)
	woke := make(chan int, cwWaiters)
	var wg sync.WaitGroup
	for i := 0; i < cwWaiters; i++ {
		pay := &cwPayload{id: i, val: seed + int64(i)*23, tag: fmt.Sprintf("w%d", i)}
		wg.Add(1)
		go func(i int, pay *cwPayload) {
			defer wg.Done()
			waiting <- i
			<-child.Done()
			if !errors.Is(context.Cause(child), errChild) {
				ck(false, fmt.Sprintf("waiter %d cause=%v want=%v", i, context.Cause(child), errChild))
			}
			if child.Err() != context.Canceled {
				ck(false, fmt.Sprintf("waiter %d Err()=%v want=Canceled", i, child.Err()))
			}
			if !pay.valid(seed) {
				ck(false, fmt.Sprintf("waiter %d payload corrupted after Done", i))
			}
			woke <- i
		}(i, pay)
	}
	for i := 0; i < cwWaiters; i++ {
		<-waiting
	}
	gcStress()
	cancelChild(errChild)
	for i := 0; i < cwWaiters; i++ {
		<-woke
	}
	wg.Wait()

	if sibling.Err() != nil || context.Cause(sibling) != nil {
		ck(false, "sibling must remain live before parent cancel")
	}
	cancelParent(errParent)
	if !errors.Is(context.Cause(parent), errParent) {
		ck(false, fmt.Sprintf("parent cause=%v want=%v", context.Cause(parent), errParent))
	}
	if !errors.Is(context.Cause(child), errChild) {
		ck(false, "child first cause must win over later parent cause")
	}
	if !errors.Is(context.Cause(sibling), errParent) {
		ck(false, fmt.Sprintf("sibling cause=%v want=%v", context.Cause(sibling), errParent))
	}
	if !errors.Is(context.Cause(grand), errChild) {
		ck(false, fmt.Sprintf("grandchild cause=%v want=%v", context.Cause(grand), errChild))
	}
	if child.Err() != context.Canceled || sibling.Err() != context.Canceled {
		ck(false, "canceled parent/child Err mismatch")
	}

	// 过期 deadline / 负 timeout 的 cause 必须保留，Err 为 DeadlineExceeded。
	dl, cancelDL := context.WithDeadlineCause(context.Background(),
		time.Now().Add(-time.Minute), errDeadline)
	defer cancelDL()
	<-dl.Done()
	if dl.Err() != context.DeadlineExceeded {
		ck(false, fmt.Sprintf("deadline Err()=%v want=DeadlineExceeded", dl.Err()))
	}
	if !errors.Is(context.Cause(dl), errDeadline) {
		ck(false, fmt.Sprintf("deadline cause=%v want=%v", context.Cause(dl), errDeadline))
	}

	tc, cancelTC := context.WithTimeoutCause(context.Background(), -time.Second, errTimeout)
	defer cancelTC()
	<-tc.Done()
	if tc.Err() != context.DeadlineExceeded {
		ck(false, fmt.Sprintf("timeout Err()=%v want=DeadlineExceeded", tc.Err()))
	}
	if !errors.Is(context.Cause(tc), errTimeout) {
		ck(false, fmt.Sprintf("timeout cause=%v want=%v", context.Cause(tc), errTimeout))
	}

	// nil cause 回退到 context.Canceled。
	nc, cancelNC := context.WithCancelCause(context.Background())
	cancelNC(nil)
	if nc.Err() != context.Canceled {
		ck(false, "nil-cause Err mismatch")
	}
	if context.Cause(nc) != context.Canceled {
		ck(false, fmt.Sprintf("nil-cause Cause=%v want=Canceled", context.Cause(nc)))
	}

	// 父已取消后创建的子 context：继承父 cause 且不被后续 cancel 覆盖。
	pc, cancelPC := context.WithCancelCause(context.Background())
	cancelPC(errParent)
	cc, cancelCC := context.WithCancelCause(pc)
	cancelCC(errChild)
	defer cancelCC(nil)
	if !errors.Is(context.Cause(cc), errParent) {
		ck(false, fmt.Sprintf("pre-canceled parent cause=%v want=%v", context.Cause(cc), errParent))
	}
	if cc.Err() != context.Canceled {
		ck(false, "pre-canceled child Err mismatch")
	}

	gcStress()
	fmt.Printf("waiters=%d causes=4 deadlines=2\n", cwWaiters)
	finish("l02_context_cause_chain")
}
