// L05: sync.Pool 复用 strings.Builder + bytes.Buffer：600 轮取用、Reset、追加
// 变长分块、快照、归还；池被 GC 清空也不影响已归还/租用对象。另保留 8 个
// 租用中的对象跨 GC 校验，并在多次 Reset 后确认无旧内容串入。
package main

import (
	"bytes"
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
	bpCycles = 600
	bpChunks = 40
	bpLeased = 8
)

var (
	failed    int32
	gcSink    [][]byte
	buildPool = sync.Pool{New: func() interface{} { return &buildPair{} }}
)

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

type buildPair struct {
	sb strings.Builder
	bb bytes.Buffer
}

func bpChunk(cycle, idx int, seed uint64) string {
	n := 8 + int((seed+uint64(cycle)*7+uint64(idx)*11)%48)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((seed*5 + uint64(cycle)*29 + uint64(idx)*13 + uint64(i)*7) % 251)
	}
	return string(b)
}

type bpSnap struct {
	s   string
	b   []byte
	crc uint32
	n   int
}

func bpWritePair(p *buildPair, cycle int, seed uint64) (uint32, int) {
	p.sb.Reset()
	p.bb.Reset()
	var crc uint32
	total := 0
	for idx := 0; idx < bpChunks; idx++ {
		chunk := bpChunk(cycle, idx, seed)
		_, errS := p.sb.WriteString(chunk)
		_, errB := p.bb.WriteString(chunk)
		ckf(errS == nil && errB == nil, "cycle=%d chunk=%d write error", cycle, idx)
		crc = crc32.Update(crc, crc32.IEEETable, []byte(chunk))
		total += len(chunk)
	}
	return crc, total
}

func main() {
	seed := loadSeed()
	snaps := make([]bpSnap, 0, bpCycles)

	for cycle := 0; cycle < bpCycles; cycle++ {
		p := buildPool.Get().(*buildPair)
		p.sb.Reset()
		p.bb.Reset()
		ckf(p.sb.Len() == 0 && p.bb.Len() == 0, "cycle=%d pooled builder not reset", cycle)

		crc, total := bpWritePair(p, cycle, seed)
		ckf(p.sb.Len() == total, "cycle=%d builder len=%d want=%d", cycle, p.sb.Len(), total)
		ckf(p.bb.Len() == total, "cycle=%d buffer len=%d want=%d", cycle, p.bb.Len(), total)
		ckf(p.sb.Cap() >= p.sb.Len(), "cycle=%d builder cap < len", cycle)
		ckf(p.bb.Cap() >= p.bb.Len(), "cycle=%d buffer cap < len", cycle)

		s := p.sb.String()
		b := p.bb.Bytes()
		if len(s) != len(b) {
			ckf(false, "cycle=%d builder/buffer length differ", cycle)
		} else {
			for i := 0; i < len(s); i += 97 {
				if s[i] != b[i] {
					ckf(false, "cycle=%d builder/buffer byte differ at=%d", cycle, i)
					break
				}
			}
		}
		ckf(crc32.ChecksumIEEE([]byte(s)) == crc, "cycle=%d builder checksum mismatch", cycle)
		ckf(crc32.ChecksumIEEE(b) == crc, "cycle=%d buffer checksum mismatch", cycle)

		// 独立快照：Builder 的 String 只在下一次修改前有效，这里显式 Clone。
		snaps = append(snaps, bpSnap{s: strings.Clone(s), b: append([]byte(nil), b...), crc: crc, n: total})

		p.sb.Reset()
		p.bb.Reset()
		ckf(p.sb.Len() == 0 && p.bb.Len() == 0, "cycle=%d reset did not clear", cycle)
		buildPool.Put(p)

		if cycle%41 == 40 {
			gcStress() // 可能清空池，后续 Get 走 New 或旧对象，两者都必须已 Reset
		}
	}

	// 租用中的对象：从池取出后不归还，跨 GC 校验内容仍有效。
	leased := make([]*buildPair, 0, bpLeased)
	leasedCRC := make([]uint32, bpLeased)
	leasedLen := make([]int, bpLeased)
	for i := 0; i < bpLeased; i++ {
		p := buildPool.Get().(*buildPair)
		p.sb.Reset()
		p.bb.Reset()
		crc, total := bpWritePair(p, 10000+i, seed)
		leased = append(leased, p)
		leasedCRC[i] = crc
		leasedLen[i] = total
		if i%2 == 1 {
			gcStress()
		}
	}
	gcStress()
	for i, p := range leased {
		ckf(p.sb.Len() == leasedLen[i], "leased=%d builder len=%d want=%d", i, p.sb.Len(), leasedLen[i])
		ckf(p.bb.Len() == leasedLen[i], "leased=%d buffer len=%d want=%d", i, p.bb.Len(), leasedLen[i])
		ckf(crc32.ChecksumIEEE([]byte(p.sb.String())) == leasedCRC[i], "leased=%d builder checksum", i)
		ckf(crc32.ChecksumIEEE(p.bb.Bytes()) == leasedCRC[i], "leased=%d buffer checksum", i)
	}
	for _, p := range leased {
		p.sb.Reset()
		p.bb.Reset()
		buildPool.Put(p)
	}

	// 最终重置后只写一个短串，确认池化对象没有把旧内容带进来。
	for k := 0; k < 32; k++ {
		p := buildPool.Get().(*buildPair)
		p.sb.Reset()
		p.bb.Reset()
		p.sb.WriteString("X")
		p.bb.WriteString("X")
		ckf(p.sb.String() == "X" && p.bb.String() == "X", "post-reset bleed k=%d builder=%q buffer=%q", k, p.sb.String(), p.bb.String())
		p.sb.Reset()
		p.bb.Reset()
		buildPool.Put(p)
		if k%8 == 7 {
			gcStress()
		}
	}

	gcStress()
	for i := range snaps {
		ckf(len(snaps[i].s) == snaps[i].n, "snapshot=%d builder len after GC", i)
		ckf(len(snaps[i].b) == snaps[i].n, "snapshot=%d buffer len after GC", i)
		ckf(crc32.ChecksumIEEE([]byte(snaps[i].s)) == snaps[i].crc, "snapshot=%d builder checksum after GC", i)
		ckf(crc32.ChecksumIEEE(snaps[i].b) == snaps[i].crc, "snapshot=%d buffer checksum after GC", i)
	}

	finish("l05_builder_pool", seed)
}
