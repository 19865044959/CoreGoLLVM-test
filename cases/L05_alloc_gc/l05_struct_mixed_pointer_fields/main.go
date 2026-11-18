// L05: 混合指针字段 struct 的大数组：每个元素含 *node/string/[]byte/map/any/func/
// uintptr/标量；跨 GC 与部分字段置 nil 后校验精确的字段位图扫描，
// 归一化子切片保留旧数组，最后复制 pin 跨 GC 校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const mixedCount = 2500

var (
	failed int32
	gcSink [][]byte
)

type mnode struct {
	id    int64
	label string
}

type mixed struct {
	seq  int64
	f64  float64
	p    *mnode
	name string
	buf  []byte
	tbl  map[string]int64
	any  interface{}
	fn   func(int64) int64
	up   uintptr
	pad  [2]int64
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

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func bufLen(i int, seed uint64) int {
	return 8 + (i*13+int(seed))%56
}

func bufByte(i, k int, seed uint64) byte {
	return byte((uint64(i)*17 + uint64(k)*7 + seed*3 + 1) % 251)
}

func makeMixed(i int, seed uint64) mixed {
	var p *mnode
	if i%5 != 0 {
		p = &mnode{id: int64(i)*11 + 1, label: fmt.Sprintf("n-%05d", i)}
	}
	n := bufLen(i, seed)
	b := make([]byte, n)
	for k := range b {
		b[k] = bufByte(i, k, seed)
	}
	var tbl map[string]int64
	if i%7 == 0 {
		tbl = map[string]int64{"a": int64(i), "b": int64(i*2 + 1)}
	}
	var any interface{}
	switch i % 4 {
	case 0:
		any = int64(i) * 5
	case 1:
		any = fmt.Sprintf("any-%05d", i)
	case 2:
		any = p // 可能为 typed-nil *mnode，接口本身非 nil
	}
	var fn func(int64) int64
	if i%6 == 0 {
		cl := p
		fn = func(d int64) int64 {
			if cl == nil {
				return int64(i)*2 + d
			}
			return int64(i)*2 + d + int64(len(cl.label))
		}
	}
	return mixed{
		seq:  int64(i)*3 + int64(seed%7),
		f64:  float64(i) * 0.5,
		p:    p,
		name: fmt.Sprintf("mix-%05d", i),
		buf:  b,
		tbl:  tbl,
		any:  any,
		fn:   fn,
		up:   uintptr(i)*64 + 0x1000,
		pad:  [2]int64{int64(i), -int64(i)},
	}
}

func checkMixed(m *mixed, i int, seed uint64, nilPhase bool) bool {
	if m.seq != int64(i)*3+int64(seed%7) ||
		m.f64 != float64(i)*0.5 ||
		m.name != fmt.Sprintf("mix-%05d", i) ||
		m.up != uintptr(i)*64+0x1000 ||
		m.pad[0] != int64(i) || m.pad[1] != -int64(i) {
		return false
	}
	if nilPhase && i%4 == 0 {
		return m.p == nil && m.buf == nil && m.tbl == nil && m.any == nil && m.fn == nil
	}
	if i%5 == 0 {
		if m.p != nil {
			return false
		}
	} else {
		if m.p == nil || m.p.id != int64(i)*11+1 || m.p.label != fmt.Sprintf("n-%05d", i) {
			return false
		}
	}
	n := bufLen(i, seed)
	if len(m.buf) != n {
		return false
	}
	for k := 0; k < n; k++ {
		if m.buf[k] != bufByte(i, k, seed) {
			return false
		}
	}
	if i%7 == 0 {
		if m.tbl == nil || len(m.tbl) != 2 || m.tbl["a"] != int64(i) || m.tbl["b"] != int64(i*2+1) {
			return false
		}
	} else if m.tbl != nil {
		return false
	}
	switch i % 4 {
	case 0:
		v, ok := m.any.(int64)
		if !ok || v != int64(i)*5 {
			return false
		}
	case 1:
		v, ok := m.any.(string)
		if !ok || v != fmt.Sprintf("any-%05d", i) {
			return false
		}
	case 2:
		v, ok := m.any.(*mnode)
		if !ok || v != m.p {
			return false
		}
	default:
		if m.any != nil {
			return false
		}
	}
	if i%6 == 0 {
		if m.fn == nil {
			return false
		}
		want := int64(i)*2 + 9
		if m.p != nil {
			want += int64(len(m.p.label))
		}
		if m.fn(9) != want {
			return false
		}
	} else if m.fn != nil {
		return false
	}
	return true
}

func main() {
	seed := loadSeed()

	ms := make([]mixed, mixedCount)
	for i := 0; i < mixedCount; i++ {
		ms[i] = makeMixed(i, seed)
		if i%500 == 0 {
			gcStress()
		}
	}
	gcStress()
	for i := 0; i < mixedCount; i++ {
		if !checkMixed(&ms[i], i, seed, false) {
			ckf(false, "element i=%d corrupted after fill+GC", i)
			break
		}
	}

	// 每 4 个清空全部指针字段：GC 位图不得把标量/uintptr 误当指针处理。
	for i := 0; i < mixedCount; i += 4 {
		ms[i].p = nil
		ms[i].buf = nil
		ms[i].tbl = nil
		ms[i].any = nil
		ms[i].fn = nil
	}
	gcStress()
	for i := 0; i < mixedCount; i++ {
		nilPhase := i%4 == 0
		if !checkMixed(&ms[i], i, seed, nilPhase) {
			ckf(false, "element i=%d corrupted after nil phase", i)
			break
		}
	}

	// 保留归一化子切片，丢弃整个大数组：旧元素靠子切片保活。
	start := mixedCount / 3
	keep := ms[start : start+96]
	var pins []mixed
	for i := 0; i < mixedCount; i += 97 {
		pins = append(pins, ms[i])
	}
	ms = nil
	for i := 0; i < 16; i++ {
		junk := make([]byte, 512<<10)
		junk[0] = byte(i)
		gcSink = append(gcSink[:0], junk)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()

	for k := range keep {
		i := start + k
		if !checkMixed(&keep[k], i, seed, i%4 == 0) {
			ckf(false, "kept index=%d corrupted after array drop", i)
			break
		}
	}
	for k := range pins {
		i := k * 97
		if !checkMixed(&pins[k], i, seed, i%4 == 0) {
			ckf(false, "pinned index=%d corrupted after array drop", i)
			break
		}
	}
	runtime.KeepAlive(keep)
	runtime.KeepAlive(pins)

	finish("l05_struct_mixed_pointer_fields", seed)
}
