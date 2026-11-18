// L13: 记录缓冲重叠压缩：原地删除连续区间时 copy 前移/后移，指针切片与 []byte 同步校验。
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type crev struct {
	id      int
	payload []byte
}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

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

func recLen(id, seed int) int { return 1 + (id*7+seed)%61 }

func payloadByte(id, i, seed int) byte { return byte(id*13 + i*29 + seed) }

func buildRecBuf(ids []int, seed int) []byte {
	n := 0
	for _, id := range ids {
		n += 4 + recLen(id, seed)
	}
	buf := make([]byte, 0, n)
	var hdr [4]byte
	for _, id := range ids {
		l := recLen(id, seed)
		binary.BigEndian.PutUint16(hdr[0:2], uint16(id))
		binary.BigEndian.PutUint16(hdr[2:4], uint16(l))
		buf = append(buf, hdr[:]...)
		for i := 0; i < l; i++ {
			buf = append(buf, payloadByte(id, i, seed))
		}
	}
	return buf
}

// compactRecBuf 原地前移保留记录；w<r 时 copy 的源/目的重叠。
func compactRecBuf(buf []byte, del func(int) bool, seed int) ([]byte, []int) {
	w, r := 0, 0
	kept := make([]int, 0, 512)
	for r < len(buf) {
		if r+4 > len(buf) {
			ckSeed(false, "compact: truncated header")
			return buf[:w], kept
		}
		id := int(binary.BigEndian.Uint16(buf[r : r+2]))
		l := int(binary.BigEndian.Uint16(buf[r+2 : r+4]))
		n := 4 + l
		if l < 1 || r+n > len(buf) {
			ckSeed(false, fmt.Sprintf("compact: bad record id=%d len=%d", id, l))
			return buf[:w], kept
		}
		if !del(id) {
			if w != r {
				copy(buf[w:w+n], buf[r:r+n])
			}
			for i := 0; i < l; i++ {
				if buf[w+4+i] != payloadByte(id, i, seed) {
					ckSeed(false, fmt.Sprintf("compact: payload corrupted id=%d off=%d", id, i))
					break
				}
			}
			w += n
			kept = append(kept, id)
		}
		r += n
	}
	return buf[:w], kept
}

func parseRecBuf(buf []byte, seed int) ([]int, bool) {
	ids := make([]int, 0, 512)
	off := 0
	for off < len(buf) {
		if off+4 > len(buf) {
			return ids, false
		}
		id := int(binary.BigEndian.Uint16(buf[off : off+2]))
		l := int(binary.BigEndian.Uint16(buf[off+2 : off+4]))
		if l < 1 || off+4+l > len(buf) {
			return ids, false
		}
		for i := 0; i < l; i++ {
			if buf[off+4+i] != payloadByte(id, i, seed) {
				return ids, false
			}
		}
		ids = append(ids, id)
		off += 4 + l
	}
	return ids, true
}

func newCrev(id, seed int) *crev {
	p := make([]byte, 8+id%24)
	for i := range p {
		p[i] = byte(id*7 + i*11 + seed)
	}
	return &crev{id: id, payload: p}
}

func validCrev(c *crev, id, seed int) bool {
	if c == nil || c.id != id || len(c.payload) != 8+id%24 {
		return false
	}
	for i := range c.payload {
		if c.payload[i] != byte(id*7+i*11+seed) {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	seed := int((curSeed%97 + 97) % 97)

	// 1) 字节记录缓冲：连续区间删除导致大段重叠前移。
	allIDs := make([]int, 0, 600)
	for id := 0; id < 600; id++ {
		allIDs = append(allIDs, id)
	}
	buf := buildRecBuf(allIDs, seed)
	ids := append([]int(nil), allIDs...)
	ck(len(buf) > 10000, "compact: test buffer too small")

	passes := []func(int) bool{
		func(id int) bool { return id%5 == 0 || (id*31+seed)%7 == 0 },
		func(id int) bool { return id >= 120 && id < 240 },
		func(id int) bool { return id < 50 },
		func(id int) bool { return id >= 520 },
	}
	for pi, del := range passes {
		oldLen := len(buf)
		oldIDs := append([]int(nil), ids...)
		kept := make([]int, 0, len(ids))
		for _, id := range ids {
			if !del(id) {
				kept = append(kept, id)
			}
		}
		var gotIDs []int
		buf, gotIDs = compactRecBuf(buf, del, seed)
		ckSeed(equalInts(gotIDs, kept), fmt.Sprintf("compact: kept ids mismatch pass=%d", pi))
		ckSeed(len(buf) < oldLen || len(kept) == len(oldIDs), fmt.Sprintf("compact: buffer did not shrink pass=%d", pi))
		ref := buildRecBuf(kept, seed)
		ckSeed(bytes.Equal(buf, ref), fmt.Sprintf("compact: buffer mismatch pass=%d", pi))
		parsed, ok := parseRecBuf(buf, seed)
		ckSeed(ok && equalInts(parsed, kept), fmt.Sprintf("compact: parse mismatch pass=%d", pi))
		ids = kept
		gcStress()
		parsed, ok = parseRecBuf(buf, seed)
		ckSeed(ok && equalInts(parsed, ids), fmt.Sprintf("compact: parse after GC mismatch pass=%d", pi))
	}
	ck(len(ids) > 0, "compact: all records deleted unexpectedly")

	// 2) 右移重叠 copy：在前部插入 9 字节头，dst>src（独立副本，不污染主缓冲）。
	oldCopy := append([]byte(nil), buf...)
	const hdrLen = 9
	shiftBuf := append([]byte(nil), buf...)
	shiftBuf = append(shiftBuf, make([]byte, hdrLen)...)
	copy(shiftBuf[hdrLen:], shiftBuf[:len(oldCopy)])
	copy(shiftBuf[:hdrLen], []byte("HDR-v1\n\x00\x00"))
	ckSeed(bytes.Equal(shiftBuf[hdrLen:], oldCopy), "compact: right-shift overlap changed body")
	ckSeed(bytes.Equal(shiftBuf[:hdrLen], []byte("HDR-v1\n\x00\x00")), "compact: header corrupted")
	gcStress()
	ckSeed(bytes.Equal(shiftBuf[hdrLen:], oldCopy), "compact: right-shift body after GC")

	// 3) 指针切片重叠压缩：按连续 run copy 指针，尾部清零后 GC。
	byID := make([]*crev, 420)
	ps := make([]*crev, 420)
	pids := make([]int, 420)
	for id := 0; id < 420; id++ {
		byID[id] = newCrev(id, seed)
		ps[id] = byID[id]
		pids[id] = id
	}
	gcStress()
	for pi := 0; pi < 4; pi++ {
		var del func(int) bool
		switch pi {
		case 0:
			del = func(id int) bool { return (id*17+seed)%6 == 0 }
		case 1:
			del = func(id int) bool { return id >= 60 && id < 160 }
		case 2:
			del = func(id int) bool { return id < 30 }
		case 3:
			del = func(id int) bool { return id >= 380 }
		}
		full := ps
		w, r := 0, 0
		kept := make([]int, 0, len(pids))
		var runs int
		for r < len(full) {
			for r < len(full) && del(pids[r]) {
				r++
			}
			start := r
			for r < len(full) && !del(pids[r]) {
				r++
			}
			cnt := r - start
			if cnt > 0 {
				copy(full[w:w+cnt], full[start:start+cnt])
				kept = append(kept, pids[start:start+cnt]...)
				w += cnt
				runs++
			}
		}
		oldLen := len(full)
		for i := w; i < oldLen; i++ {
			full[i] = nil
		}
		ps = full[:w]
		pids = kept
		ckSeed(len(ps) == 0 || ps[len(ps)-1] != nil, "compact ptr: tail not zeroed")
		for i, id := range pids {
			ckSeed(ps[i] == byID[id] && validCrev(ps[i], id, seed), fmt.Sprintf("compact ptr: identity bad pass=%d idx=%d", pi, i))
		}
		gcStress()
		for i, id := range pids {
			ckSeed(ps[i] == byID[id] && validCrev(ps[i], id, seed), fmt.Sprintf("compact ptr: after GC bad pass=%d idx=%d", pi, i))
		}
		ckSeed(runs > 0, fmt.Sprintf("compact ptr: no runs pass=%d", pi))
	}
	ck(len(pids) > 0 && len(ps) == len(pids), "compact ptr: final size")

	// 4) 幂等性：对已压缩缓冲再压缩“无删除”不改变内容。
	before := append([]byte(nil), buf...)
	buf2, kept2 := compactRecBuf(buf, func(int) bool { return false }, seed)
	ckSeed(bytes.Equal(buf2, before), "compact: no-delete compaction changed buffer")
	ckSeed(len(kept2) == len(ids), "compact: no-delete kept count")
	gcStress()
	ckSeed(bytes.Equal(buf, before), "compact: no-delete buffer after GC")

	runtime.KeepAlive(buf)
	runtime.KeepAlive(ps)
	runtime.KeepAlive(byID)
	finish("l13_overlap_compact_ranges")
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
