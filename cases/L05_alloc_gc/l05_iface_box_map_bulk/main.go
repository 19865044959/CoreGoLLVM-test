// L05: 接口装箱 + 宽 map。map[int64]any 写入 16000 个六类装箱值（int64、
// string、[]byte、按值大 struct、*node、闭包），GC 后逐个 type switch 还原；
// 再覆盖 1/4、删除 1/5，GC 后校验；整表丢弃后仅靠 pin 与旧接口值跨 GC，
// 最后重建新表。重点验证 map bucket 中的接口数据字、itab 与大 struct 装箱副本。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const ibKeys = 16000

var (
	failed    int32
	gcSink    [][]byte
	ibPtrs    []*ibNode
	ibBigPtrs []*ibNode
)

type ibNode struct {
	id   int
	name string
}

type ibBig struct {
	fixed [160]byte
	tag   string
	p     *ibNode
	n     int64
}

type ibOld struct {
	id   int
	kind int
	v    any
}

type ibPin struct {
	id int
	v  any
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

func ibKind(i int, seed uint64) int {
	return int((uint64(i)*7 + seed*3) % 6)
}

func ibInt(i int, seed uint64) int64 {
	return int64(i)*7 + int64(seed)*1000
}

func ibStr(i int, seed uint64) string {
	return fmt.Sprintf("s-%d-%d", i, seed%97)
}

func ibBodyLen(i int) int {
	return 8 + i%40
}

func ibBodyByte(i, j int, seed uint64) byte {
	return byte((seed*23 + uint64(i)*13 + uint64(j)*7) % 251)
}

func ibBigByte(i, j int, seed uint64) byte {
	return byte((seed*31 + uint64(i)*17 + uint64(j)*3) % 251)
}

func ibMakeBig(i int, seed uint64, p *ibNode) ibBig {
	var b ibBig
	for j := range b.fixed {
		b.fixed[j] = ibBigByte(i, j, seed)
	}
	b.tag = fmt.Sprintf("big-%d", i)
	b.p = p
	b.n = int64(i)*3 + int64(seed)
	return b
}

func ibExpected(i int, seed uint64) any {
	switch ibKind(i, seed) {
	case 0:
		return ibInt(i, seed)
	case 1:
		return ibStr(i, seed)
	case 2:
		n := ibBodyLen(i)
		b := make([]byte, n)
		for j := range b {
			b[j] = ibBodyByte(i, j, seed)
		}
		return b
	case 3:
		return ibMakeBig(i, seed, ibBigPtrs[i])
	case 4:
		return ibPtrs[i]
	default:
		ii := i
		return func() int64 { return ibInt(ii, seed) }
	}
}

func ibCheckKind(what string, i int, v any, seed uint64) {
	switch ibKind(i, seed) {
	case 0:
		x, ok := v.(int64)
		ckf(ok, "%s i=%d kind0 not int64 but %T", what, i, v)
		ckf(x == ibInt(i, seed), "%s i=%d int64=%d want=%d", what, i, x, ibInt(i, seed))
	case 1:
		s, ok := v.(string)
		ckf(ok, "%s i=%d kind1 not string but %T", what, i, v)
		ckf(s == ibStr(i, seed), "%s i=%d string=%q want=%q", what, i, s, ibStr(i, seed))
	case 2:
		b, ok := v.([]byte)
		ckf(ok, "%s i=%d kind2 not []byte but %T", what, i, v)
		if !ok {
			return
		}
		ckf(len(b) == ibBodyLen(i), "%s i=%d body len=%d want=%d", what, i, len(b), ibBodyLen(i))
		for j := range b {
			if b[j] != ibBodyByte(i, j, seed) {
				ckf(false, "%s i=%d body byte=%d", what, i, j)
				break
			}
		}
	case 3:
		b, ok := v.(ibBig)
		ckf(ok, "%s i=%d kind3 not ibBig but %T", what, i, v)
		if !ok {
			return
		}
		ckf(b.tag == fmt.Sprintf("big-%d", i), "%s i=%d big tag=%q", what, i, b.tag)
		ckf(b.p == ibBigPtrs[i], "%s i=%d big inner pointer lost", what, i)
		ckf(b.n == int64(i)*3+int64(seed), "%s i=%d big n=%d", what, i, b.n)
		for j := 0; j < len(b.fixed); j += 17 {
			if b.fixed[j] != ibBigByte(i, j, seed) {
				ckf(false, "%s i=%d big fixed byte=%d", what, i, j)
				break
			}
		}
	case 4:
		p, ok := v.(*ibNode)
		ckf(ok, "%s i=%d kind4 not *ibNode but %T", what, i, v)
		ckf(p == ibPtrs[i], "%s i=%d pointer identity lost", what, i)
		if ok && p != nil {
			ckf(p.id == i && p.name == fmt.Sprintf("n-%d", i), "%s i=%d node content", what, i)
		}
	case 5:
		f, ok := v.(func() int64)
		ckf(ok, "%s i=%d kind5 not func but %T", what, i, v)
		if ok {
			ckf(f() == ibInt(i, seed), "%s i=%d closure=%d want=%d", what, i, f(), ibInt(i, seed))
		}
	}
}

func ibCheckCurrent(what string, i int, v any, seed uint64) {
	switch {
	case i%4 == 0:
		x, ok := v.(int64)
		ckf(ok, "%s i=%d overwritten not int64 but %T", what, i, v)
		ckf(x == int64(i)*13+int64(seed)*7, "%s i=%d overwritten int64=%d", what, i, x)
	case i%4 == 1:
		s, ok := v.(string)
		ckf(ok, "%s i=%d overwritten not string but %T", what, i, v)
		ckf(s == fmt.Sprintf("ovr-%d-%d", i, seed%89), "%s i=%d overwritten string=%q", what, i, s)
	default:
		ibCheckKind(what, i, v, seed)
	}
}

func main() {
	seed := loadSeed()
	ibPtrs = make([]*ibNode, ibKeys)
	ibBigPtrs = make([]*ibNode, ibKeys)

	m := make(map[int64]any)
	for i := 0; i < ibKeys; i++ {
		kind := ibKind(i, seed)
		if kind == 3 {
			ibBigPtrs[i] = &ibNode{id: i, name: fmt.Sprintf("bigp-%d", i)}
		}
		if kind == 4 {
			ibPtrs[i] = &ibNode{id: i, name: fmt.Sprintf("n-%d", i)}
		}
		m[int64(i)] = ibExpected(i, seed)
	}
	ckf(len(m) == ibKeys, "map len=%d want=%d", len(m), ibKeys)

	gcStress()
	for i := 0; i < ibKeys; i++ {
		v, ok := m[int64(i)]
		ckf(ok, "build+GC i=%d missing", i)
		if ok {
			ibCheckKind("build+GC", i, v, seed)
		}
	}

	// 覆盖 1/4（保留旧接口值），删除 1/5。
	oldAny := make([]ibOld, 0, ibKeys/2)
	for i := 0; i < ibKeys; i++ {
		v, ok := m[int64(i)]
		ckf(ok, "pre-overwrite i=%d missing", i)
		switch {
		case i%4 == 0:
			oldAny = append(oldAny, ibOld{id: i, kind: ibKind(i, seed), v: v})
			m[int64(i)] = int64(i)*13 + int64(seed)*7
		case i%4 == 1:
			oldAny = append(oldAny, ibOld{id: i, kind: ibKind(i, seed), v: v})
			m[int64(i)] = fmt.Sprintf("ovr-%d-%d", i, seed%89)
		}
	}
	deleted := 0
	for i := 0; i < ibKeys; i++ {
		if i%5 == 0 {
			delete(m, int64(i))
			deleted++
		}
	}
	ckf(len(m) == ibKeys-deleted, "after update+delete len=%d want=%d", len(m), ibKeys-deleted)

	// pin 一份更新后的接口值（避开删除项）。
	var pins []ibPin
	for i := 0; i < ibKeys; i++ {
		if i%5 == 0 {
			continue
		}
		if i%997 == 7 {
			pins = append(pins, ibPin{id: i, v: m[int64(i)]})
		}
	}
	ckf(len(pins) > 0, "no pins collected")

	gcStress()
	for i := 0; i < ibKeys; i++ {
		v, ok := m[int64(i)]
		if i%5 == 0 {
			ckf(!ok, "deleted i=%d still present", i)
			continue
		}
		ckf(ok, "post-update i=%d missing", i)
		if ok {
			ibCheckCurrent("post-update+GC", i, v, seed)
		}
	}
	for _, o := range oldAny {
		ibCheckKind("old-any+GC", o.id, o.v, seed)
	}
	for _, p := range pins {
		ibCheckCurrent("pin+GC", p.id, p.v, seed)
	}

	// 整表丢弃，pin 与旧接口值仍有效。
	m = nil
	gcStress()
	for _, o := range oldAny {
		ibCheckKind("old-any after drop+GC", o.id, o.v, seed)
	}
	for _, p := range pins {
		ibCheckCurrent("pin after drop+GC", p.id, p.v, seed)
	}

	// 重建新表，验证与旧 pin 互不影响。
	m2 := make(map[int64]any, ibKeys/2)
	for i := 0; i < ibKeys; i += 2 {
		m2[int64(i)] = ibExpected(i, seed)
	}
	gcStress()
	for i := 0; i < ibKeys; i += 2 {
		v, ok := m2[int64(i)]
		ckf(ok, "rebuilt i=%d missing", i)
		if ok {
			ibCheckKind("rebuilt+GC", i, v, seed)
		}
	}
	for _, p := range pins {
		ibCheckCurrent("pin after rebuild+GC", p.id, p.v, seed)
	}

	// typed-nil 接口语义。
	var np *ibNode
	var anyNil any = np
	ckf(anyNil != nil, "typed-nil *ibNode boxed to nil interface")
	p, ok := anyNil.(*ibNode)
	ckf(ok && p == nil, "typed-nil assertion got ok=%v p=%v", ok, p)

	finish("l05_iface_box_map_bulk", seed)
}
