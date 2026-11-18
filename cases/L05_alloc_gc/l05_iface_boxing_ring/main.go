// L05: 非空接口装箱：四类具体值（值接收者/指针接收者/切片/错误）装箱进
// ring 与 map，逐代覆盖旧装箱对象并显式 GC；校验动态派发、类型断言、
// typed-nil 接口语义与 pin 住的老装箱值跨 GC 完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	ringSize = 512
	gens     = 10
	mapCount = 512
)

var (
	failed int32
	gcSink [][]byte
)

type valuer interface {
	Value() int64
	Kind() string
}

type bnode struct {
	id   int64
	name string
}

type boxInt struct {
	id int
	v  int64
}

func (b boxInt) Value() int64 { return b.v }
func (b boxInt) Kind() string { return "int" }

type boxPtr struct {
	id  int
	v   int64
	tag *bnode
}

func (b *boxPtr) Value() int64 {
	if b == nil {
		return -1
	}
	return b.v
}

func (b *boxPtr) Kind() string {
	if b == nil {
		return "nilptr"
	}
	return "ptr"
}

type boxBytes struct {
	id int
	b  []byte
}

func (b boxBytes) Value() int64 {
	var s int64
	for _, x := range b.b {
		s += int64(x)
	}
	return s
}
func (b boxBytes) Kind() string { return "bytes" }

type boxErr struct {
	code int
}

func (e *boxErr) Error() string {
	if e == nil {
		return "nilerr"
	}
	return fmt.Sprintf("e%04d", e.code)
}
func (e *boxErr) Value() int64 {
	if e == nil {
		return -9
	}
	return int64(e.code)
}
func (e *boxErr) Kind() string {
	if e == nil {
		return "nilerr"
	}
	return "err"
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

func bytesLen(k, gen int) int {
	return 8 + (k*7+gen*13)%37
}

func bytesVal(k, gen, i int, seed uint64) byte {
	return byte((uint64(k)*17 + uint64(gen)*31 + uint64(i)*7 + seed*5 + 1) % 251)
}

func makeValuer(k, gen int, seed uint64) valuer {
	switch k % 4 {
	case 0:
		return boxInt{id: k, v: int64(k*7+gen*13) + int64(seed%19)}
	case 1:
		return &boxPtr{
			id:  k,
			v:   int64(k*11+gen*3) - int64(seed%11),
			tag: &bnode{id: int64(k)*5 + int64(gen), name: fmt.Sprintf("tag-%05d-%02d", k, gen)},
		}
	case 2:
		n := bytesLen(k, gen)
		b := make([]byte, n)
		for i := range b {
			b[i] = bytesVal(k, gen, i, seed)
		}
		return boxBytes{id: k, b: b}
	default:
		return &boxErr{code: k%1000 + gen}
	}
}

func wantValue(k, gen int, seed uint64) int64 {
	switch k % 4 {
	case 0:
		return int64(k*7+gen*13) + int64(seed%19)
	case 1:
		return int64(k*11+gen*3) - int64(seed%11)
	case 2:
		var s int64
		n := bytesLen(k, gen)
		for i := 0; i < n; i++ {
			s += int64(bytesVal(k, gen, i, seed))
		}
		return s
	default:
		return int64(k%1000 + gen)
	}
}

func wantKind(k int) string {
	switch k % 4 {
	case 0:
		return "int"
	case 1:
		return "ptr"
	case 2:
		return "bytes"
	default:
		return "err"
	}
}

func checkValuer(v valuer, k, gen int, seed uint64) bool {
	if v == nil || v.Kind() != wantKind(k) || v.Value() != wantValue(k, gen, seed) {
		return false
	}
	switch k % 4 {
	case 1:
		p, ok := v.(*boxPtr)
		if !ok || p == nil || p.tag == nil || p.tag.id != int64(k)*5+int64(gen) ||
			p.tag.name != fmt.Sprintf("tag-%05d-%02d", k, gen) {
			return false
		}
	case 2:
		b, ok := v.(boxBytes)
		if !ok || len(b.b) != bytesLen(k, gen) {
			return false
		}
		for i := range b.b {
			if b.b[i] != bytesVal(k, gen, i, seed) {
				return false
			}
		}
	}
	return true
}

func main() {
	seed := loadSeed()

	type pin struct {
		v   valuer
		k   int
		gen int
	}
	var pins []pin

	ring := make([]valuer, ringSize)
	for gen := 0; gen < gens; gen++ {
		for k := 0; k < ringSize; k++ {
			ring[k] = makeValuer(gen*ringSize+k, gen, seed)
		}
		gcStress()
		for k := 0; k < ringSize; k++ {
			if !checkValuer(ring[k], gen*ringSize+k, gen, seed) {
				ckf(false, "gen=%d slot=%d corrupted after GC", gen, k)
				break
			}
		}
		pins = append(pins, pin{v: ring[gen%ringSize], k: gen*ringSize + gen%ringSize, gen: gen})
	}

	// 旧代装箱值已被新代覆盖为垃圾；pin 住的少数值必须仍完整。
	for i := range pins {
		if !checkValuer(pins[i].v, pins[i].k, pins[i].gen, seed) {
			ckf(false, "pinned gen=%d k=%d corrupted", pins[i].gen, pins[i].k)
		}
	}

	// map[string]valuer 中混合具体类型，按确定 key 校验动态派发。
	m := make(map[string]valuer, mapCount)
	for k := 0; k < mapCount; k++ {
		m[fmt.Sprintf("k-%05d", k)] = makeValuer(k, 77, seed)
	}
	gcStress()
	for k := 0; k < mapCount; k++ {
		if !checkValuer(m[fmt.Sprintf("k-%05d", k)], k, 77, seed) {
			ckf(false, "map boxed key=%d corrupted after GC", k)
			break
		}
	}
	m = nil
	gcStress()

	// typed-nil：赋给非空接口后接口非 nil，方法需能安全处理 nil 接收者。
	var np *boxPtr
	var iv valuer = np
	ck(iv != nil, "typed nil *boxPtr in valuer must be non-nil interface")
	ck(iv.Kind() == "nilptr" && iv.Value() == -1, "typed nil boxPtr method dispatch")
	var ei error = (*boxErr)(nil)
	ck(ei != nil, "typed nil *boxErr in error must be non-nil interface")
	if ei != nil && ei.Error() != "nilerr" {
		ck(false, "typed nil boxErr Error()")
	}
	if ev, ok := ei.(valuer); ok {
		ck(ev.Kind() == "nilerr" && ev.Value() == -9, "typed nil error converted to valuer")
	} else {
		ck(false, "typed nil error lost valuer method set")
	}
	var anyNil interface{} = np
	ck(anyNil != nil, "typed nil pointer in empty interface must be non-nil")
	if p, ok := anyNil.(*boxPtr); !ok || p != nil {
		ck(false, "typed nil pointer assertion")
	}
	var empty valuer
	ck(empty == nil, "uninitialized valuer must be nil")

	runtime.KeepAlive(ring)
	runtime.KeepAlive(pins)

	finish("l05_iface_boxing_ring", seed)
}
