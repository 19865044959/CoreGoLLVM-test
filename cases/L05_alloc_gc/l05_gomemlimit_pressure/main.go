// L05: GOMEMLIMIT 环境下的分配压力（只读 env，不设置 memory limit）：
// 保持一波波 live 对象跨显式 GC，同时持续制造大块短命垃圾，校验内容、
// GC 计数增长、TotalAlloc 增长，且环境变量在运行期间未被改写。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	defaultRounds = 2
	baseLive      = 320
	payloadSize   = 8192
)

var (
	failed int32
	gcSink [][]byte
)

type liveObj struct {
	id    int
	label string
	data  []byte
	next  *liveObj
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		n = hi
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func liveLen(id, round int, seed uint64) int {
	return payloadSize + (id*97+round*131+int(seed))%2048
}

func liveByte(id, round, i int, seed uint64) byte {
	base := uint64(id)*31 + uint64(round)*101 + seed*13 + 3
	return byte((base + uint64(i)*7) % 251)
}

func makeLive(id, round int, seed uint64) *liveObj {
	n := liveLen(id, round, seed)
	b := make([]byte, n)
	for i := range b {
		b[i] = liveByte(id, round, i, seed)
	}
	return &liveObj{id: id, label: fmt.Sprintf("live-%03d-%02d", id, round), data: b}
}

func checkLive(o *liveObj, id, round int, seed uint64) bool {
	if o == nil || o.id != id || o.label != fmt.Sprintf("live-%03d-%02d", id, round) {
		return false
	}
	n := liveLen(id, round, seed)
	if len(o.data) != n {
		return false
	}
	if o.data[0] != liveByte(id, round, 0, seed) || o.data[n-1] != liveByte(id, round, n-1, seed) {
		return false
	}
	for i := 0; i < n; i += 97 {
		if o.data[i] != liveByte(id, round, i, seed) {
			return false
		}
	}
	return true
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", defaultRounds, 1, 6)

	// 只读：记录当前 GOMEMLIMIT 的文本值，绝不调用 debug.SetMemoryLimit。
	limitEnv := os.Getenv("GOMEMLIMIT")
	liveCount := baseLive
	if limitEnv != "" {
		// 受限环境下保持更小的常驻集合，压力仍由短命垃圾提供。
		liveCount = 224
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	type pin struct {
		o     *liveObj
		round int
	}
	var pins []pin

	for round := 0; round < rounds; round++ {
		objs := make([]*liveObj, liveCount)
		for i := 0; i < liveCount; i++ {
			objs[i] = makeLive(i, round, seed)
		}
		for i := 0; i < liveCount; i++ {
			objs[i].next = objs[(i*7+3)%liveCount]
		}
		gcStress()
		for i := 0; i < liveCount; i++ {
			if !checkLive(objs[i], i, round, seed) {
				ckf(false, "round=%d live id=%d corrupted after fill+GC", round, i)
				break
			}
			nid := (i*7 + 3) % liveCount
			if objs[i].next == nil || objs[i].next.id != nid {
				ckf(false, "round=%d live id=%d next pointer corrupted", round, i)
				break
			}
		}

		// 大量短命垃圾：每块 256KB，共 12MB；GOGC=1/GOMEMLIMIT 环境下触发频繁 GC。
		for k := 0; k < 48; k++ {
			junk := make([]byte, 256<<10)
			junk[(round*48+k)%len(junk)] = byte(k)
			gcSink = append(gcSink[:0], junk)
		}
		gcSink = gcSink[:0]
		runtime.GC()
		runtime.GC()

		for i := 0; i < liveCount; i++ {
			if !checkLive(objs[i], i, round, seed) {
				ckf(false, "round=%d live id=%d corrupted after garbage+GC", round, i)
				break
			}
		}

		// 释放一半 live 对象，保留的 next 环仍必须有效。
		for i := 0; i < liveCount; i += 2 {
			objs[i] = nil
		}
		gcStress()
		kept := 0
		for i := 1; i < liveCount; i += 2 {
			if !checkLive(objs[i], i, round, seed) {
				ckf(false, "round=%d kept id=%d corrupted after half drop", round, i)
				break
			}
			kept++
		}
		ckf(kept == liveCount/2, "round=%d kept=%d want=%d", round, kept, liveCount/2)

		for i := 0; i < liveCount; i += 63 {
			if objs[i] != nil {
				pins = append(pins, pin{o: objs[i], round: round})
			}
		}
		objs = nil
		for k := 0; k < 8; k++ {
			junk := make([]byte, 1<<20)
			junk[0] = byte(round*8 + k)
			gcSink = append(gcSink[:0], junk)
		}
		gcSink = gcSink[:0]
		runtime.GC()
		runtime.GC()
	}

	for k := range pins {
		if !checkLive(pins[k].o, pins[k].o.id, pins[k].round, seed) {
			ckf(false, "pinned round=%d id=%d corrupted after live-set drop", pins[k].round, pins[k].o.id)
		}
	}

	runtime.ReadMemStats(&after)
	ckf(after.TotalAlloc > before.TotalAlloc, "TotalAlloc did not grow: %d -> %d", before.TotalAlloc, after.TotalAlloc)
	ckf(after.HeapAlloc > 0, "HeapAlloc is zero after pressure run")
	ckf(after.NumGC >= before.NumGC+uint32(rounds*2), "explicit GCs not observed: before=%d after=%d", before.NumGC, after.NumGC)
	ck(os.Getenv("GOMEMLIMIT") == limitEnv, "GOMEMLIMIT env changed during run")
	runtime.KeepAlive(pins)

	finish("l05_gomemlimit_pressure", seed)
}
