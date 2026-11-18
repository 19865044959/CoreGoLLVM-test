// L07: 运行时对齐探测与分派。按 src/dst 数据指针的低 3 位选择 8/4/2/1 字节拷贝路径，
// 对 64 种对齐组合与多种长度验证结果逐字节一致，且不越界写保护字节。
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

const (
	kindWord = iota
	kindU32
	kindU16
	kindByte
)

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
			b := make([]byte, 4096+i*13)
			b[0] = byte(i)
			b[len(b)-1] = byte(199 - i)
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

func alignClass(p unsafe.Pointer) int {
	return int(uintptr(p) & 7)
}

func copyDispatch(dst, src []byte) (int, bool) {
	if len(dst) < len(src) {
		return -1, false
	}
	n := len(src)
	if n == 0 {
		return kindByte, true
	}
	sc := uintptr(unsafe.Pointer(unsafe.SliceData(src))) & 7
	dc := uintptr(unsafe.Pointer(unsafe.SliceData(dst))) & 7
	switch {
	case sc == 0 && dc == 0:
		i := 0
		for ; i+8 <= n; i += 8 {
			*(*uint64)(unsafe.Pointer(&dst[i])) = *(*uint64)(unsafe.Pointer(&src[i]))
		}
		for ; i < n; i++ {
			dst[i] = src[i]
		}
		return kindWord, true
	case sc&3 == 0 && dc&3 == 0:
		i := 0
		for ; i+4 <= n; i += 4 {
			*(*uint32)(unsafe.Pointer(&dst[i])) = *(*uint32)(unsafe.Pointer(&src[i]))
		}
		for ; i < n; i++ {
			dst[i] = src[i]
		}
		return kindU32, true
	case sc&1 == 0 && dc&1 == 0:
		i := 0
		for ; i+2 <= n; i += 2 {
			*(*uint16)(unsafe.Pointer(&dst[i])) = *(*uint16)(unsafe.Pointer(&src[i]))
		}
		for ; i < n; i++ {
			dst[i] = src[i]
		}
		return kindU16, true
	default:
		copy(dst[:n], src[:n])
		return kindByte, true
	}
}

func wantKind(sc, dc int) int {
	if sc == 0 && dc == 0 {
		return kindWord
	}
	if sc&3 == 0 && dc&3 == 0 {
		return kindU32
	}
	if sc&1 == 0 && dc&1 == 0 {
		return kindU16
	}
	return kindByte
}

func findClass(b []byte, cls int) int {
	for i := 0; i < 64; i++ {
		if alignClass(unsafe.Pointer(&b[i])) == cls {
			return i
		}
	}
	return -1
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

	src := make([]byte, 512)
	dst := make([]byte, 512)
	for i := range src {
		src[i] = byte((i*37 + int(seed)*11 + 3) & 0xff)
	}
	initDst := func() {
		for i := range dst {
			dst[i] = 0xaa
		}
	}
	initDst()

	n := 19 + int(seed%13)
	var used [4]int

	// 1) 同对齐类：0..7 每种组合都走对应宽度路径，结果与源逐字节一致。
	for cls := 0; cls < 8; cls++ {
		so := findClass(src, cls)
		do := findClass(dst, cls)
		ck(so >= 0 && do >= 0, "class offset found cls="+strconv.Itoa(cls))
		initDst()
		kind, ok := copyDispatch(dst[do:do+n], src[so:so+n])
		ck(ok, "same-class dispatch ok cls="+strconv.Itoa(cls))
		ck(kind == wantKind(cls, cls), "same-class kind cls="+strconv.Itoa(cls))
		used[kind]++
		ck(bytes.Equal(dst[do:do+n], src[so:so+n]), "same-class bytes cls="+strconv.Itoa(cls))
		ck(dst[do+n] == 0xaa, "same-class tail guard cls="+strconv.Itoa(cls))
		if do > 0 {
			ck(dst[do-1] == 0xaa, "same-class head guard cls="+strconv.Itoa(cls))
		}
	}
	for k := 0; k < 4; k++ {
		ck(used[k] > 0, "dispatch path used k="+strconv.Itoa(k))
	}

	// 2) 64 种 src/dst 对齐组合：内容一致、分派类型完全由低 3 位决定。
	var mixed [4]int
	for sc := 0; sc < 8; sc++ {
		for dc := 0; dc < 8; dc++ {
			so := findClass(src, sc)
			do := findClass(dst, dc)
			initDst()
			kind, ok := copyDispatch(dst[do:do+n], src[so:so+n])
			ck(ok, "mixed dispatch ok sc="+strconv.Itoa(sc)+" dc="+strconv.Itoa(dc))
			ck(kind == wantKind(sc, dc), "mixed kind sc="+strconv.Itoa(sc)+" dc="+strconv.Itoa(dc))
			mixed[kind]++
			ck(bytes.Equal(dst[do:do+n], src[so:so+n]),
				"mixed bytes sc="+strconv.Itoa(sc)+" dc="+strconv.Itoa(dc))
			ck(dst[do+n] == 0xaa, "mixed tail guard sc="+strconv.Itoa(sc)+" dc="+strconv.Itoa(dc))
		}
	}
	for k := 0; k < 4; k++ {
		ck(mixed[k] > 0, "mixed path used k="+strconv.Itoa(k))
	}

	// 3) 长度边界：0/1/2/7/8/9/15/16/17/31/64/65，尾字节必须逐字节补齐。
	for _, ln := range []int{0, 1, 2, 7, 8, 9, 15, 16, 17, 31, 64, 65} {
		for _, offs := range [][2]int{{0, 0}, {1, 1}, {0, 1}, {1, 0}, {4, 0}, {0, 4}, {3, 6}} {
			so, do := offs[0], offs[1]
			initDst()
			kind, ok := copyDispatch(dst[do:do+ln], src[so:so+ln])
			ck(ok, "edge dispatch ln="+strconv.Itoa(ln))
			_ = kind
			ck(bytes.Equal(dst[do:do+ln], src[so:so+ln]), "edge bytes ln="+strconv.Itoa(ln))
			ck(dst[do+ln] == 0xaa, "edge guard ln="+strconv.Itoa(ln))
		}
	}

	// 4) 目标短于源必须拒绝，且不得写入。
	initDst()
	ck(len(dst) >= 4, "dst large enough")
	kind, ok := copyDispatch(dst[:3], src[:4])
	ck(!ok && kind == -1, "short dst rejected")
	ck(dst[0] == 0xaa && dst[1] == 0xaa && dst[2] == 0xaa && dst[3] == 0xaa, "rejected copy writes nothing")

	// 5) 对齐探测在全地址空间成立：偏移 0..7 恰好覆盖 8 个类各一次。
	seen := make([]bool, 8)
	for off := 0; off < 8; off++ {
		seen[alignClass(unsafe.Pointer(&src[off]))] = true
	}
	for cls := 0; cls < 8; cls++ {
		ck(seen[cls], "offset ladder covers class cls="+strconv.Itoa(cls))
	}

	// 6) 保留对齐的字指针跨显式 GC；前后保护字节不被分派拷贝破坏。
	const keepOff = 256
	pattern := uint64(0x1122334455667788) ^ uint64(seed)
	*(*uint64)(unsafe.Pointer(&dst[keepOff])) = pattern
	dst[keepOff-1] = 0x5a
	dst[keepOff+8] = 0x5a
	kp := (*uint64)(unsafe.Pointer(&dst[keepOff]))
	src = nil
	dst = nil
	gcStress()
	ck(*kp == pattern, "aligned word survives GC")
	view := unsafe.Slice((*byte)(unsafe.Pointer(kp)), 8)
	var re uint64
	for b := 0; b < 8; b++ {
		re |= uint64(view[b]) << (8 * uint(b))
	}
	ck(re == pattern, "byte view after GC")
	ck(*(*byte)(unsafe.Add(unsafe.Pointer(kp), -1)) == 0x5a, "guard before word after GC")
	ck(*(*byte)(unsafe.Add(unsafe.Pointer(kp), 8)) == 0x5a, "guard after word after GC")

	finish("l07_alignment_probe_dispatch")
}
