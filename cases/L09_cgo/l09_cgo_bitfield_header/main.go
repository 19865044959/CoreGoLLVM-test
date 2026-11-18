// L09: C 位域协议头：4/4/8/16 + 16/3/13 + 6/20/6 位字段，含 6 位有符号
// 字段。cgo 不能直接访问 bit-field，全部经 C 辅助函数读写；校验掩码、
// 字段独立性、字节往返与跨 GC 稳定。C 不保留 Go 指针。
package main

/*
#include <stdint.h>
#include <string.h>

typedef struct {
	uint32_t ver : 4;
	uint32_t ihl : 4;
	uint32_t tos : 8;
	uint32_t total_len : 16;
	uint32_t id : 16;
	uint32_t flags : 3;
	uint32_t frag_off : 13;
	int32_t delta : 6;
	uint32_t win : 20;
	uint32_t pad : 6;
} hdr_t;

static size_t c_hdr_size(void) { return sizeof(hdr_t); }

static void c_hdr_set(hdr_t *h, uint32_t ver, uint32_t ihl, uint32_t tos,
		      uint32_t total_len, uint32_t id, uint32_t flags,
		      uint32_t frag_off, int32_t delta, uint32_t win,
		      uint32_t pad) {
	h->ver = ver;
	h->ihl = ihl;
	h->tos = tos;
	h->total_len = total_len;
	h->id = id;
	h->flags = flags;
	h->frag_off = frag_off;
	h->delta = delta;
	h->win = win;
	h->pad = pad;
}

static void c_hdr_zero(hdr_t *h) { memset(h, 0, sizeof(hdr_t)); }
static void c_hdr_set_tos(hdr_t *h, uint32_t v) { h->tos = v; }
static void c_hdr_set_delta(hdr_t *h, int32_t v) { h->delta = v; }
static void c_hdr_set_win(hdr_t *h, uint32_t v) { h->win = v; }
static void c_hdr_set_pad(hdr_t *h, uint32_t v) { h->pad = v; }

static uint32_t c_hdr_ver(const hdr_t *h) { return h->ver; }
static uint32_t c_hdr_ihl(const hdr_t *h) { return h->ihl; }
static uint32_t c_hdr_tos(const hdr_t *h) { return h->tos; }
static uint32_t c_hdr_total_len(const hdr_t *h) { return h->total_len; }
static uint32_t c_hdr_id(const hdr_t *h) { return h->id; }
static uint32_t c_hdr_flags(const hdr_t *h) { return h->flags; }
static uint32_t c_hdr_frag_off(const hdr_t *h) { return h->frag_off; }
static int32_t c_hdr_delta(const hdr_t *h) { return h->delta; }
static uint32_t c_hdr_win(const hdr_t *h) { return h->win; }
static uint32_t c_hdr_pad(const hdr_t *h) { return h->pad; }

static void c_hdr_to_bytes(const hdr_t *h, uint8_t *out, size_t n) {
	if (n >= sizeof(hdr_t)) {
		memcpy(out, h, sizeof(hdr_t));
	}
}

static int32_t c_hdr_from_bytes(hdr_t *h, const uint8_t *in, size_t n) {
	if (n < sizeof(hdr_t)) {
		return -1;
	}
	memcpy(h, in, sizeof(hdr_t));
	return 0;
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
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

type hdrVals struct {
	ver, ihl, tos, totalLen, id, flags, fragOff int32
	delta                                       int32
	win, pad                                    int32
}

func setHdr(h *C.hdr_t, v hdrVals) {
	C.c_hdr_set(h, C.uint32_t(v.ver), C.uint32_t(v.ihl), C.uint32_t(v.tos),
		C.uint32_t(v.totalLen), C.uint32_t(v.id), C.uint32_t(v.flags),
		C.uint32_t(v.fragOff), C.int32_t(v.delta), C.uint32_t(v.win), C.uint32_t(v.pad))
}

func hdrOf(h *C.hdr_t) hdrVals {
	return hdrVals{
		ver:      int32(C.c_hdr_ver(h)),
		ihl:      int32(C.c_hdr_ihl(h)),
		tos:      int32(C.c_hdr_tos(h)),
		totalLen: int32(C.c_hdr_total_len(h)),
		id:       int32(C.c_hdr_id(h)),
		flags:    int32(C.c_hdr_flags(h)),
		fragOff:  int32(C.c_hdr_frag_off(h)),
		delta:    int32(C.c_hdr_delta(h)),
		win:      int32(C.c_hdr_win(h)),
		pad:      int32(C.c_hdr_pad(h)),
	}
}

func main() {
	ck(uint64(unsafe.Sizeof(C.hdr_t{})) == uint64(C.c_hdr_size()), "sizeof hdr_t")

	var h C.hdr_t
	maxv := hdrVals{ver: 15, ihl: 15, tos: 255, totalLen: 65535, id: 65535,
		flags: 7, fragOff: 8191, delta: -32, win: 1<<20 - 1, pad: 63}
	setHdr(&h, maxv)
	ck(hdrOf(&h) == maxv, "max values roundtrip")

	minv := hdrVals{ver: 0, ihl: 0, tos: 0, totalLen: 0, id: 0, flags: 0,
		fragOff: 0, delta: 31, win: 0, pad: 0}
	setHdr(&h, minv)
	ck(hdrOf(&h) == minv, "zero + signed delta max roundtrip")

	// 6 位有符号字段边界。
	for _, d := range []int32{-32, -31, -1, 0, 1, 30, 31} {
		C.c_hdr_set_delta(&h, C.int32_t(d))
		ck(int32(C.c_hdr_delta(&h)) == d, fmt.Sprintf("signed delta %d", d))
	}

	// 无符号位域赋值按 2^width 取模；同时验证邻字段不受影响。
	base := hdrVals{ver: 3, ihl: 4, tos: 5, totalLen: 6, id: 7, flags: 2,
		fragOff: 8, delta: -9, win: 10, pad: 11}
	setHdr(&h, base)
	C.c_hdr_set_tos(&h, 300)
	ck(int32(C.c_hdr_tos(&h)) == 44, "tos 300 masks to 44")
	C.c_hdr_set_win(&h, 2000000)
	ck(int32(C.c_hdr_win(&h)) == 951424, "win 2000000 masks to 951424")
	C.c_hdr_set_pad(&h, 100)
	ck(int32(C.c_hdr_pad(&h)) == 36, "pad 100 masks to 36")
	want := base
	want.tos, want.win, want.pad = 44, 951424, 36
	ck(hdrOf(&h) == want, "masking leaves neighbors intact")

	// 全宽掩码值。
	C.c_hdr_set(&h, 22, 17, 300, 70000, 70000, 11, 9000, 0, 2000000, 100)
	want = hdrVals{ver: 22 & 0xF, ihl: 17 & 0xF, tos: 300 & 0xFF, totalLen: 70000 & 0xFFFF,
		id: 70000 & 0xFFFF, flags: 11 & 0x7, fragOff: 9000 & 0x1FFF, delta: 0,
		win: 2000000 & 0xFFFFF, pad: 100 & 0x3F}
	ck(hdrOf(&h) == want, "all fields mask to width")

	// 字段独立性：改一个，其余保持。
	C.c_hdr_set_tos(&h, 77)
	ck(int32(C.c_hdr_tos(&h)) == 77 && int32(C.c_hdr_ihl(&h)) == int32(want.ihl) &&
		int32(C.c_hdr_total_len(&h)) == int32(want.totalLen), "tos independence")

	// 结构体 <-> 字节往返（C memcpy 自身），验证位域打包可逆。
	nbytes := int(C.c_hdr_size())
	buf := make([]byte, nbytes)
	for round := 0; round < 32; round++ {
		v := hdrVals{
			ver:      int32(round % 16),
			ihl:      int32((round * 3) % 16),
			tos:      int32((round * 11) % 256),
			totalLen: int32((round*997 + 13) % 65536),
			id:       int32((round*313 + 7) % 65536),
			flags:    int32(round % 8),
			fragOff:  int32((round * 733) % 8192),
			delta:    int32(round%64) - 32,
			win:      int32((round * 12345) % (1 << 20)),
			pad:      int32(round % 64),
		}
		setHdr(&h, v)
		C.c_hdr_to_bytes(&h, (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf)))
		C.c_hdr_zero(&h)
		ck(hdrOf(&h) == (hdrVals{delta: 0}), "zero after memset")
		rc := int32(C.c_hdr_from_bytes(&h, (*C.uint8_t)(unsafe.Pointer(&buf[0])), C.size_t(len(buf))))
		ck(rc == 0, fmt.Sprintf("from_bytes rc round=%d", round))
		ck(hdrOf(&h) == v, fmt.Sprintf("byte roundtrip round=%d", round))
	}

	// 缓冲区不足时不改写结构体。
	small := make([]byte, 1)
	C.c_hdr_zero(&h)
	rc := int32(C.c_hdr_from_bytes(&h, (*C.uint8_t)(unsafe.Pointer(&small[0])), 1))
	ck(rc == -1 && hdrOf(&h) == (hdrVals{delta: 0}), "short buffer rejected")

	// 跨 GC 稳定。
	setHdr(&h, maxv)
	for round := 0; round < 8; round++ {
		gcStress()
		ck(hdrOf(&h) == maxv, fmt.Sprintf("hdr after GC round=%d", round))
		ck(int32(C.c_hdr_delta(&h)) == -32, fmt.Sprintf("delta after GC round=%d", round))
		ck(len(buf) == nbytes, fmt.Sprintf("buf length round=%d", round))
		runtime.KeepAlive(h)
		runtime.KeepAlive(buf)
	}

	finish("l09_cgo_bitfield_header")
}
