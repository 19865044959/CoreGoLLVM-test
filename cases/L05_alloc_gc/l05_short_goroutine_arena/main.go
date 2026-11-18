// L05: 短命 goroutine 的局部 arena。每波启动 24 个 goroutine，各自分配一块
// 4×8KB 的动态本地 arena，按 8B 对齐写入 40 条含 data/name/next 链的记录；
// worker 在 GC 前后重算链摘要，并只把一条记录的深拷贝与摘要交回主协程。
// 主协程在 worker 退出后 GC，验证返回值不依赖已消亡的 arena。默认 2 波，
// TORTURE_ROUNDS 可放大。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	gaWorkers   = 24
	gaRecs      = 40
	gaBlockSize = 8 << 10
	gaBlocks    = 4
	gaOwned     = 7
)

var (
	failed int32
	gcSink [][]byte
)

type gaRec struct {
	id   int
	name string
	data []byte
	next *gaRec
}

type gaResult struct {
	wave      int
	worker    int
	digest    uint64
	count     int
	ownedID   int
	ownedName string
	ownedData []byte
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
		return hi
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

func gcStress() {
	for i := 0; i < 20; i++ {
		b := make([]byte, 16<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func gaRecSize(worker, i int, seed uint64) int {
	return 24 + int((seed+uint64(worker)*7+uint64(i)*13)%96)
}

func gaByte(wave, worker, i, j int, seed uint64) byte {
	return byte((seed*17 + uint64(wave)*43 + uint64(worker)*29 + uint64(i)*11 + uint64(j)*3) % 251)
}

func gaID(wave, worker, i int) int {
	return wave*1000000 + worker*1000 + i
}

func gaName(worker, i int) string {
	return fmt.Sprintf("w%d-r%d", worker, i)
}

func gaDigest(start *gaRec) (uint64, int) {
	d := uint64(1469598103934665603)
	count := 0
	for r := start; r != nil; r = r.next {
		count++
		d = d*1099511628211 + uint64(r.id)*131
		for i := 0; i < len(r.name); i++ {
			d = d*1099511628211 + uint64(r.name[i])
		}
		for i := range r.data {
			d = d*1099511628211 + uint64(r.data[i])
		}
	}
	return d, count
}

func gaExpected(wave, worker int, seed uint64) (uint64, int, int, string, []byte) {
	recs := make([]*gaRec, gaRecs)
	for i := 0; i < gaRecs; i++ {
		n := gaRecSize(worker, i, seed)
		data := make([]byte, n)
		for j := range data {
			data[j] = gaByte(wave, worker, i, j, seed)
		}
		r := &gaRec{id: gaID(wave, worker, i), name: gaName(worker, i), data: data}
		if i > 0 {
			r.next = recs[i-1]
		}
		recs[i] = r
	}
	d, count := gaDigest(recs[gaRecs-1])
	own := recs[gaOwned]
	return d, count, own.id, own.name, append([]byte(nil), own.data...)
}

func gaRun(wave, worker int, seed uint64) gaResult {
	arena := make([]byte, gaBlocks*gaBlockSize)
	recs := make([]*gaRec, gaRecs)
	off := 0
	for i := 0; i < gaRecs; i++ {
		n := gaRecSize(worker, i, seed)
		off = (off + 7) &^ 7
		if off+n > len(arena) {
			ckf(false, "wave=%d worker=%d rec=%d local arena overflow", wave, worker, i)
			break
		}
		data := arena[off : off+n]
		for j := range data {
			data[j] = gaByte(wave, worker, i, j, seed)
		}
		off += n
		r := &gaRec{id: gaID(wave, worker, i), name: gaName(worker, i), data: data}
		if i > 0 {
			r.next = recs[i-1]
		}
		recs[i] = r
	}
	d1, c1 := gaDigest(recs[gaRecs-1])

	// 在局部 arena 仍存活时显式 GC + 垃圾分配。
	garbage := make([][]byte, 0, 8)
	for pass := 0; pass < 2; pass++ {
		garbage = garbage[:0]
		for j := 0; j < 8; j++ {
			garbage = append(garbage, make([]byte, 8<<10))
		}
		runtime.GC()
	}

	for i := 0; i < gaRecs; i++ {
		r := recs[i]
		if r == nil {
			ckf(false, "wave=%d worker=%d rec=%d nil", wave, worker, i)
			continue
		}
		ckf(r.id == gaID(wave, worker, i), "wave=%d worker=%d rec=%d id=%d", wave, worker, i, r.id)
		ckf(r.name == gaName(worker, i), "wave=%d worker=%d rec=%d name", wave, worker, i)
		ckf(len(r.data) == gaRecSize(worker, i, seed), "wave=%d worker=%d rec=%d data len", wave, worker, i)
		if len(r.data) > 0 {
			ckf(r.data[0] == gaByte(wave, worker, i, 0, seed), "wave=%d worker=%d rec=%d first byte", wave, worker, i)
			ckf(r.data[len(r.data)-1] == gaByte(wave, worker, i, len(r.data)-1, seed), "wave=%d worker=%d rec=%d last byte", wave, worker, i)
		}
		if i > 0 {
			ckf(r.next == recs[i-1], "wave=%d worker=%d rec=%d next link", wave, worker, i)
		} else {
			ckf(r.next == nil, "wave=%d worker=%d first rec has next", wave, worker)
		}
	}
	d2, c2 := gaDigest(recs[gaRecs-1])
	ckf(d1 == d2 && c1 == c2, "wave=%d worker=%d digest changed across local GC", wave, worker)
	own := recs[gaOwned]
	runtime.KeepAlive(arena)
	runtime.KeepAlive(recs)

	return gaResult{
		wave:      wave,
		worker:    worker,
		digest:    d2,
		count:     c2,
		ownedID:   own.id,
		ownedName: strings.Clone(own.name),
		ownedData: append([]byte(nil), own.data...),
	}
}

func main() {
	seed := loadSeed()
	waves := envInt("TORTURE_ROUNDS", 2, 1, 8)
	pins := make([]gaResult, 0, waves*gaWorkers)

	for wave := 0; wave < waves; wave++ {
		ch := make(chan gaResult, gaWorkers)
		for w := 0; w < gaWorkers; w++ {
			go func(worker int) {
				ch <- gaRun(wave, worker, seed)
			}(w)
		}
		results := make([]gaResult, gaWorkers)
		seen := make([]bool, gaWorkers)
		for k := 0; k < gaWorkers; k++ {
			r := <-ch
			ckf(r.wave == wave, "wave=%d result wave=%d", wave, r.wave)
			ckf(r.worker >= 0 && r.worker < gaWorkers, "wave=%d result worker=%d", wave, r.worker)
			if r.worker < 0 || r.worker >= gaWorkers {
				continue
			}
			ckf(!seen[r.worker], "wave=%d duplicate worker=%d", wave, r.worker)
			seen[r.worker] = true
			results[r.worker] = r
		}
		for w := 0; w < gaWorkers; w++ {
			ckf(seen[w], "wave=%d missing worker=%d", wave, w)
		}

		gcStress()
		for w := range results {
			r := results[w]
			ed, ec, eid, en, eb := gaExpected(wave, w, seed)
			ckf(r.digest == ed, "wave=%d worker=%d digest=%016x want=%016x", wave, w, r.digest, ed)
			ckf(r.count == ec, "wave=%d worker=%d count=%d want=%d", wave, w, r.count, ec)
			ckf(r.ownedID == eid, "wave=%d worker=%d owned id=%d want=%d", wave, w, r.ownedID, eid)
			ckf(r.ownedName == en, "wave=%d worker=%d owned name=%q want=%q", wave, w, r.ownedName, en)
			ckf(len(r.ownedData) == len(eb), "wave=%d worker=%d owned len=%d want=%d", wave, w, len(r.ownedData), len(eb))
			for j := 0; j < len(r.ownedData) && j < len(eb); j++ {
				if r.ownedData[j] != eb[j] {
					ckf(false, "wave=%d worker=%d owned byte=%d", wave, w, j)
					break
				}
			}
		}

		// 各 worker 的深拷贝互不别名。
		if len(results) > 1 && len(results[0].ownedData) > 0 && len(results[1].ownedData) > 0 {
			orig := results[0].ownedData[0]
			results[0].ownedData[0] ^= 0xff
			ckf(results[1].ownedData[0] == gaByte(wave, 1, gaOwned, 0, seed), "wave=%d owned data aliases between workers", wave)
			results[0].ownedData[0] = orig
		}
		pins = append(pins, results...)
	}

	// worker 已全部退出；仅深拷贝结果跨 GC。
	gcStress()
	for _, r := range pins {
		ed, ec, eid, en, eb := gaExpected(r.wave, r.worker, seed)
		ckf(r.digest == ed, "pin wave=%d worker=%d digest changed", r.wave, r.worker)
		ckf(r.count == ec, "pin wave=%d worker=%d count changed", r.wave, r.worker)
		ckf(r.ownedID == eid && r.ownedName == en, "pin wave=%d worker=%d owned record changed", r.wave, r.worker)
		ckf(len(r.ownedData) == len(eb), "pin wave=%d worker=%d owned len changed", r.wave, r.worker)
		for j := 0; j < len(r.ownedData) && j < len(eb); j++ {
			if r.ownedData[j] != eb[j] {
				ckf(false, "pin wave=%d worker=%d owned byte=%d", r.wave, r.worker, j)
				break
			}
		}
	}
	ckf(len(pins) == waves*gaWorkers, "pins=%d want=%d", len(pins), waves*gaWorkers)

	finish("l05_short_goroutine_arena", seed)
}
