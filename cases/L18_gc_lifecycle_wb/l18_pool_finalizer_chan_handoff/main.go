// L18: sync.Pool + finalizer + 老 channel 交接。池对象先进老缓冲 chan，
// 跨多轮 GC 后取出一半到全局 slice，再取出剩余；部分对象放回池但保留外部
// 引用。断言：完全可达期间 finalizer 标记恒为 0 且 payload 完好；丢弃池与
// 全部外部引用后，所有对象最终被终结。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	pfN        = 128
	pfSplit    = 64
	pfChanCap  = 256
	pfDataN    = 24
	pfFlagCap  = 512
	pfWaitTime = 5 * time.Second
)

type pfObj struct {
	id   int32
	ver  int64
	data [pfDataN]byte
}

type pfHubT struct {
	ch   chan *pfObj
	held []*pfObj
}

var (
	failed int32
	gcSink [][]byte
	pfPool *sync.Pool
	pfHub  pfHubT
	pfIDs  []int32
	pfFlag []int32
	pfNext int32
	pfSalt int64
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

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func pfByte(ver int64, id int32, i int) byte {
	return byte((int(ver)*53 + int(id)*19 + i*7 + int(pfSalt)) % 251)
}

//go:noinline
func pfNew() *pfObj {
	id := atomic.AddInt32(&pfNext, 1)
	o := &pfObj{id: id}
	runtime.SetFinalizer(o, func(q *pfObj) {
		if q.id > 0 && int(q.id) < len(pfFlag) {
			atomic.StoreInt32(&pfFlag[q.id], 1)
		}
	})
	return o
}

//go:noinline
func pfMakePool() *sync.Pool {
	return &sync.Pool{New: func() any { return pfNew() }}
}

//go:noinline
func pfFillChan(n int) {
	for i := 0; i < n; i++ {
		o := pfPool.Get().(*pfObj)
		o.ver = int64(i + 1)
		for j := range o.data {
			o.data[j] = pfByte(o.ver, o.id, j)
		}
		pfHub.ch <- o
		pfIDs = append(pfIDs, o.id)
	}
}

//go:noinline
func pfRecvToHeld(n int) {
	for i := 0; i < n; i++ {
		pfHub.held = append(pfHub.held, <-pfHub.ch)
	}
}

//go:noinline
func pfPutHalf(n int) {
	for i := 0; i < n; i++ {
		pfPool.Put(pfHub.held[i])
	}
}

func pfCountFinalized(ids []int32) int {
	n := 0
	for _, id := range ids {
		if atomic.LoadInt32(&pfFlag[id]) != 0 {
			n++
		}
	}
	return n
}

func pfCheckPayload(seed int64) bool {
	for _, o := range pfHub.held {
		if o == nil || o.ver <= 0 {
			ck(false, fmt.Sprintf("held object header corrupted (seed=%d)", seed))
			return false
		}
		for j := range o.data {
			if o.data[j] != pfByte(o.ver, o.id, j) {
				ck(false, fmt.Sprintf("held object id=%d ver=%d data corrupted at %d (seed=%d)", o.id, o.ver, j, seed))
				return false
			}
		}
	}
	return true
}

func pfWaitFinalized(ids []int32, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		pending := len(ids) - pfCountFinalized(ids)
		if pending == 0 || time.Now().After(deadline) {
			return pending
		}
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

func main() {
	seed := seedValue()
	pfSalt = seed % 5
	pfFlag = make([]int32, pfFlagCap)
	pfPool = pfMakePool()
	pfHub.ch = make(chan *pfObj, pfChanCap)
	gcStress()
	gcStress() // 老 hub 与 chan 晋升；池对象尚未创建

	pfFillChan(pfN)
	gcStress()
	ck(len(pfHub.ch) == pfN, fmt.Sprintf("old chan len=%d want=%d (seed=%d)", len(pfHub.ch), pfN, seed))
	ck(pfCountFinalized(pfIDs) == 0, fmt.Sprintf("finalizer ran while objects in chan (seed=%d)", seed))

	pfRecvToHeld(pfSplit)
	gcStress()
	ck(len(pfHub.ch) == pfN-pfSplit, fmt.Sprintf("chan len=%d want=%d after half recv (seed=%d)", len(pfHub.ch), pfN-pfSplit, seed))
	ck(pfCountFinalized(pfIDs) == 0, fmt.Sprintf("finalizer ran while half held/half in chan (seed=%d)", seed))
	if !pfCheckPayload(seed) {
		finish("l18_pool_finalizer_chan_handoff")
		return
	}

	pfRecvToHeld(pfN - pfSplit)
	gcStress()
	ck(len(pfHub.held) == pfN, fmt.Sprintf("held len=%d want=%d (seed=%d)", len(pfHub.held), pfN, seed))
	ck(pfCountFinalized(pfIDs) == 0, fmt.Sprintf("finalizer ran while all held (seed=%d)", seed))
	if !pfCheckPayload(seed) {
		finish("l18_pool_finalizer_chan_handoff")
		return
	}

	// 一半放回池，但外部仍持有；GC 清空池也不能终结这些对象。
	pfPutHalf(pfSplit)
	gcStress()
	ck(pfCountFinalized(pfIDs) == 0, fmt.Sprintf("finalizer ran for externally held pooled objects (seed=%d)", seed))
	if !pfCheckPayload(seed) {
		finish("l18_pool_finalizer_chan_handoff")
		return
	}

	ids := append([]int32(nil), pfIDs...)
	pfHub.held = nil
	pfHub.ch = nil
	pfIDs = nil
	pfPool = nil
	gcStress()
	pending := pfWaitFinalized(ids, pfWaitTime)
	ck(pending == 0, fmt.Sprintf("objects not finalized after release: %d/%d (seed=%d)", pending, len(ids), seed))

	finish("l18_pool_finalizer_chan_handoff")
}
