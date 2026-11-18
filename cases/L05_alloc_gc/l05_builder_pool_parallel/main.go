// L05: 并发 worker 共享 sync.Pool 的 strings.Builder。每个 worker 每轮借出、
// Reset、追加 48 个变长分块、快照后归还；借出表中的同一对象不能同时被两个
// worker 持有。池可能被并发 GC 清空，New 对象与旧对象都必须 Len==0。最后保留
// 4 个未归还的 builder 跨多次 GC 校验。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	bprWorkers = 6
	bprRounds  = 36
	bprChunks  = 48
	bprLeased  = 4
)

var (
	failed      int32
	gcSink      [][]byte
	builderPool = sync.Pool{New: func() interface{} { return new(strings.Builder) }}
	bprMu       sync.Mutex
	bprLive     = make(map[*strings.Builder]bool)
)

type bprResult struct {
	worker int
	round  int
	crc    uint32
	n      int
	snap   string
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

func bprAcquire() *strings.Builder {
	b := builderPool.Get().(*strings.Builder)
	bprMu.Lock()
	if bprLive[b] {
		ckf(false, "builder checked out twice")
	}
	bprLive[b] = true
	bprMu.Unlock()
	return b
}

func bprRelease(b *strings.Builder) {
	b.Reset()
	bprMu.Lock()
	delete(bprLive, b)
	bprMu.Unlock()
	builderPool.Put(b)
}

func bprChunk(worker, round, chunk int, seed uint64) string {
	n := 8 + int((seed+uint64(worker)*31+uint64(round)*17+uint64(chunk)*13)%40)
	buf := make([]byte, n)
	for j := range buf {
		buf[j] = byte((seed*7 + uint64(worker)*29 + uint64(round)*11 + uint64(chunk)*5 + uint64(j)*3) % 251)
	}
	return string(buf)
}

func bprExpected(worker, round int, seed uint64) (uint32, int) {
	var crc uint32
	total := 0
	for c := 0; c < bprChunks; c++ {
		s := bprChunk(worker, round, c, seed)
		crc = crc32.Update(crc, crc32.IEEETable, []byte(s))
		total += len(s)
	}
	return crc, total
}

func main() {
	seed := loadSeed()
	results := make([]bprResult, bprWorkers*bprRounds)
	var wg sync.WaitGroup

	for w := 0; w < bprWorkers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			garbage := make([][]byte, 0, 8)
			for r := 0; r < bprRounds; r++ {
				b := bprAcquire()
				ckf(b.Len() == 0, "worker=%d round=%d pooled builder len=%d", worker, r, b.Len())
				b.Reset()

				var crc uint32
				total := 0
				for c := 0; c < bprChunks; c++ {
					chunk := bprChunk(worker, r, c, seed)
					_, err := b.WriteString(chunk)
					ckf(err == nil, "worker=%d round=%d chunk=%d write error", worker, r, c)
					crc = crc32.Update(crc, crc32.IEEETable, []byte(chunk))
					total += len(chunk)
				}
				s := b.String()
				ckf(b.Len() == total, "worker=%d round=%d len=%d want=%d", worker, r, b.Len(), total)
				ckf(crc32.ChecksumIEEE([]byte(s)) == crc, "worker=%d round=%d checksum", worker, r)
				results[worker*bprRounds+r] = bprResult{
					worker: worker,
					round:  r,
					crc:    crc,
					n:      total,
					snap:   strings.Clone(s),
				}
				bprRelease(b)

				if r%9 == 8 {
					garbage = garbage[:0]
					for i := 0; i < 8; i++ {
						garbage = append(garbage, make([]byte, 8<<10))
					}
					runtime.GC() // 并发 GC 可能清空池
				}
			}
			runtime.KeepAlive(garbage)
		}(w)
	}
	wg.Wait()
	gcStress()

	for w := 0; w < bprWorkers; w++ {
		for r := 0; r < bprRounds; r++ {
			res := results[w*bprRounds+r]
			wantCRC, wantN := bprExpected(w, r, seed)
			ckf(res.worker == w && res.round == r, "result slot=%d worker=%d round=%d", w*bprRounds+r, res.worker, res.round)
			ckf(res.n == wantN, "worker=%d round=%d n=%d want=%d", w, r, res.n, wantN)
			ckf(len(res.snap) == wantN, "worker=%d round=%d snapshot len=%d want=%d", w, r, len(res.snap), wantN)
			ckf(crc32.ChecksumIEEE([]byte(res.snap)) == wantCRC, "worker=%d round=%d snapshot crc", w, r)
		}
	}

	// 租用中的 builder 跨 GC 必须保持内容，归还后 Len 归零。
	leased := make([]*strings.Builder, 0, bprLeased)
	leasedCRC := make([]uint32, 0, bprLeased)
	leasedN := make([]int, 0, bprLeased)
	for i := 0; i < bprLeased; i++ {
		b := bprAcquire()
		b.Reset()
		var crc uint32
		total := 0
		for c := 0; c < 120; c++ {
			chunk := bprChunk(100+i, 0, c, seed)
			b.WriteString(chunk)
			crc = crc32.Update(crc, crc32.IEEETable, []byte(chunk))
			total += len(chunk)
		}
		leased = append(leased, b)
		leasedCRC = append(leasedCRC, crc)
		leasedN = append(leasedN, total)
		if i%2 == 1 {
			gcStress()
		}
	}
	gcStress()
	for i, b := range leased {
		ckf(b.Len() == leasedN[i], "leased=%d len=%d want=%d", i, b.Len(), leasedN[i])
		ckf(crc32.ChecksumIEEE([]byte(b.String())) == leasedCRC[i], "leased=%d checksum", i)
	}
	for _, b := range leased {
		bprRelease(b)
	}
	for i, b := range leased {
		ck := bprAcquire()
		ckf(ck.Len() == 0, "reused leased=%d len=%d want=0", i, ck.Len())
		bprRelease(ck)
		_ = b
	}

	bprMu.Lock()
	nlive := len(bprLive)
	bprMu.Unlock()
	ckf(nlive == 0, "builders still checked out=%d", nlive)

	finish("l05_builder_pool_parallel", seed)
}
