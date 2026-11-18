// L07: 分段零拷贝的跨段边界。记录体视图直接引用同一大缓冲，覆盖记录起点/记录体
// 跨 64KiB 段边界、空记录、输入改写可见性与跨 GC 保活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

const segSize = 64 << 10

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 8192+i*19)
			b[0] = byte(i)
			b[len(b)-1] = byte(240 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > 8 {
				v = 8
			}
			return v
		}
	}
	return def
}

func pat(i int, s int64) byte {
	return byte((uint64(i)*131 + uint64(s)*17 + 13) & 0xff)
}

const (
	fnvOff   = uint32(2166136261)
	fnvPrime = uint32(16777619)
)

func fnvBytes(b []byte) uint32 {
	h := fnvOff
	for _, c := range b {
		h ^= uint32(c)
		h *= fnvPrime
	}
	return h
}

func fnvString(s string) uint32 {
	h := fnvOff
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= fnvPrime
	}
	return h
}

func wantSum(off, n int, s int64) uint32 {
	h := fnvOff
	for i := off; i < off+n; i++ {
		h ^= uint32(pat(i, s))
		h *= fnvPrime
	}
	return h
}

type recPos struct {
	off     int
	bodyOff int
	n       int
}

type recView struct {
	round   int
	off     int
	bodyOff int
	n       int
	view    string
	sum     uint32
	cross   bool
}

func putLen3(b []byte, off, n int) {
	b[off] = byte(n)
	b[off+1] = byte(n >> 8)
	b[off+2] = byte(n >> 16)
}

// genRecords 在 payload 上顺序铺记录。段边界前先用 180 字节记录推进到边界附近，
// 再放一条记录体末字节恰落在边界上的记录（必跨段）；另有记录起点恰在边界上。
func genRecords(total int, s int64) ([]byte, []recPos) {
	b := make([]byte, total)
	for i := range b {
		b[i] = pat(i, s)
	}
	recs := make([]recPos, 0, total/180+32)
	off := 0
	appendRec := func(n int) {
		if off+3 > total {
			return
		}
		if n < 0 {
			n = 0
		}
		if n > total-(off+3) {
			n = total - (off + 3)
		}
		putLen3(b, off, n)
		recs = append(recs, recPos{off: off, bodyOff: off + 3, n: n})
		off += 3 + n
	}
	for off+3 <= total {
		nextB := ((off / segSize) + 1) * segSize
		if nextB >= total {
			lens := [...]int{1, 2, 3, 7, 16, 33, 65, 129}
			appendRec(lens[len(recs)%len(lens)])
			continue
		}
		gap := nextB - off
		switch {
		case gap > 203:
			appendRec(180)
		case gap >= 4:
			// 前缀 off..off+2，记录体 off+3..nextB，末字节恰在边界上。
			appendRec(gap - 2)
		case gap == 3:
			appendRec(0)
			appendRec(2) // 记录起点恰在边界
		default:
			appendRec(1)
		}
	}
	return b, recs
}

func makeViews(b []byte, recs []recPos, s int64, round int) []recView {
	out := make([]recView, len(recs))
	for i, rp := range recs {
		var v string
		if rp.n > 0 {
			v = unsafe.String(unsafe.SliceData(b[rp.bodyOff:rp.bodyOff+rp.n]), rp.n)
		}
		cross := rp.n > 0 && rp.bodyOff/segSize != (rp.bodyOff+rp.n-1)/segSize
		out[i] = recView{
			round:   round,
			off:     rp.off,
			bodyOff: rp.bodyOff,
			n:       rp.n,
			view:    v,
			sum:     wantSum(rp.bodyOff, rp.n, s),
			cross:   cross,
		}
	}
	return out
}

func verifyAll(views []recView, stage string) {
	for i := range views {
		v := &views[i]
		s := seed + int64(v.round)*101
		ck(len(v.view) == v.n, stage+": view len i="+strconv.Itoa(i))
		ck(fnvString(v.view) == v.sum, stage+": checksum i="+strconv.Itoa(i))
		ck(v.sum == wantSum(v.bodyOff, v.n, s), stage+": expected sum i="+strconv.Itoa(i))
		ck(v.cross == (v.n > 0 && v.bodyOff/segSize != (v.bodyOff+v.n-1)/segSize),
			stage+": cross flag i="+strconv.Itoa(i))
		if v.n > 0 {
			ck(v.view[0] == pat(v.bodyOff, s), stage+": first byte i="+strconv.Itoa(i))
			mid := v.n / 2
			ck(v.view[mid] == pat(v.bodyOff+mid, s), stage+": mid byte i="+strconv.Itoa(i))
			ck(v.view[v.n-1] == pat(v.bodyOff+v.n-1, s), stage+": last byte i="+strconv.Itoa(i))
		}
	}
}

func verifyRound(b []byte, views []recView, s int64, total int, stage string) {
	verifyAll(views, stage)

	nseg := (total + segSize - 1) / segSize
	segs := make([]string, nseg)
	for i := 0; i < nseg; i++ {
		off := i * segSize
		n := segSize
		if off+n > total {
			n = total - off
		}
		chunk := b[off : off+n]
		segs[i] = unsafe.String(unsafe.SliceData(chunk), len(chunk))
		ck(fnvString(segs[i]) == fnvBytes(chunk), stage+": segment sum i="+strconv.Itoa(i))
		ck(unsafe.Pointer(unsafe.StringData(segs[i])) == unsafe.Pointer(&b[off]), stage+": segment alias i="+strconv.Itoa(i))
	}

	crossN := 0
	covered := make([]bool, nseg)
	for i := range views {
		v := &views[i]
		if v.n > 0 {
			ck(unsafe.Pointer(unsafe.StringData(v.view)) == unsafe.Pointer(&b[v.bodyOff]),
				stage+": record aliases input i="+strconv.Itoa(i))
			firstSeg := v.bodyOff / segSize
			lastSeg := (v.bodyOff + v.n - 1) / segSize
			ck(segs[firstSeg][v.bodyOff%segSize] == v.view[0], stage+": first merges segment i="+strconv.Itoa(i))
			ck(segs[lastSeg][(v.bodyOff+v.n-1)%segSize] == v.view[v.n-1], stage+": last merges segment i="+strconv.Itoa(i))
			for bIdx := firstSeg + 1; bIdx <= lastSeg; bIdx++ {
				if bIdx >= 0 && bIdx < len(covered) {
					covered[bIdx] = true
				}
			}
			if v.cross {
				crossN++
			}
		}
	}
	ck(crossN > 0, stage+": has cross-segment records")
	for i := 1; i < nseg; i++ {
		ck(covered[i], stage+": every segment boundary crossed b="+strconv.Itoa(i))
	}

	// 段边界字节的改写必须立刻反映在跨段视图上，恢复后一致。
	var cv *recView
	for i := range views {
		if views[i].cross && views[i].n > 1 {
			cv = &views[i]
			break
		}
	}
	ck(cv != nil, stage+": cross record with n>1")
	if cv != nil {
		local := segSize - cv.bodyOff%segSize
		if local <= 0 || local >= cv.n {
			local = cv.n / 2
		}
		old := b[cv.bodyOff+local]
		b[cv.bodyOff+local] ^= 0xff
		ck(cv.view[local] != old, stage+": boundary write visible")
		b[cv.bodyOff+local] ^= 0xff
		ck(cv.view[local] == old, stage+": boundary write restored")
	}
}

func main() {
	if env := os.Getenv("UAF_SEED"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	total := (512 << 10) + int(seed%3)*(64<<10)
	rounds := tortureRounds(2)
	retained := make([]recView, 0, rounds*((total/200)+64))

	for r := 0; r < rounds; r++ {
		s := seed + int64(r)*101
		b, recs := genRecords(total, s)
		ck(len(recs) > 64, "record count round="+strconv.Itoa(r))
		views := makeViews(b, recs, s, r)
		baseSum := fnvBytes(b)
		verifyRound(b, views, s, total, "round "+strconv.Itoa(r))
		ck(fnvBytes(b) == baseSum, "payload unchanged round "+strconv.Itoa(r))

		retained = append(retained, views...)
		b = nil
		gcStress()
		verifyAll(retained, "retained after gc round "+strconv.Itoa(r))
	}

	ck(len(retained) > rounds*64, "retained record count")
	finish("l07_segment_record_span_parse")
}
