// L18: 单个大于 32KB 的大 struct 的零化与值拷贝。
// 每轮：新分配的大对象必须全零；填充年轻指针/接口/字符串后 GC 校验；按值拷贝
// 整个大 struct 后两个对象共享内部指针；reset（*p = hugeRec{}）后必须彻底零化，
// 不残留旧指针。TORTURE_ROUNDS 放大轮数，覆盖大对象 span 的复用/零化。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	hugeWords     = 4096
	hugeRawN      = 1024
	hugeDefault   = 3
	hugeMaxRounds = 24
)

type hNode struct {
	v    int64
	name string
}

type hugeRec struct {
	id    int64
	ver   int
	name  string
	n     *hNode
	ifc   interface{}
	words [hugeWords]int64
	raw   [hugeRawN]byte
}

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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > hugeMaxRounds {
		n = hugeMaxRounds
	}
	return n
}

func hugeWord(gen, i int) int64 { return int64((gen*1000003 + i*37) % 1000000007) }
func hugeByte(gen, i int) byte  { return byte((gen*29 + i*11) % 251) }

//go:noinline
func allocHuge() *hugeRec {
	return new(hugeRec)
}

//go:noinline
func fillHuge(p *hugeRec, gen int) {
	p.id = int64(gen)
	p.ver = gen
	p.name = "huge"
	p.n = &hNode{v: int64(gen * 7), name: "hn"}
	p.ifc = p.n
	for i := range p.words {
		p.words[i] = hugeWord(gen, i)
	}
	for i := range p.raw {
		p.raw[i] = hugeByte(gen, i)
	}
}

//go:noinline
func copyHuge(src *hugeRec) *hugeRec {
	dst := new(hugeRec)
	*dst = *src
	return dst
}

//go:noinline
func resetHuge(p *hugeRec) {
	*p = hugeRec{}
}

func hugeZero(p *hugeRec) bool {
	if p.id != 0 || p.ver != 0 || p.name != "" || p.n != nil || p.ifc != nil {
		return false
	}
	for _, w := range p.words {
		if w != 0 {
			return false
		}
	}
	for _, b := range p.raw {
		if b != 0 {
			return false
		}
	}
	return true
}

func hugeMatch(p *hugeRec, gen int) bool {
	if p.id != int64(gen) || p.ver != gen || p.name != "huge" {
		return false
	}
	if p.n == nil || p.n.v != int64(gen*7) || p.n.name != "hn" {
		return false
	}
	in, ok := p.ifc.(*hNode)
	if !ok || in != p.n {
		return false
	}
	for i := range p.words {
		if p.words[i] != hugeWord(gen, i) {
			return false
		}
	}
	for i := range p.raw {
		if p.raw[i] != hugeByte(gen, i) {
			return false
		}
	}
	return true
}

func main() {
	rounds := tortureRounds(hugeDefault)
	var last *hugeRec
	for r := 1; r <= rounds; r++ {
		p := allocHuge()
		if !hugeZero(p) {
			ck(false, fmt.Sprintf("fresh huge struct (round %d) not zeroed", r))
		}

		fillHuge(p, r)
		gcStress()
		if !hugeMatch(p, r) {
			ck(false, fmt.Sprintf("filled huge struct (round %d) corrupted after GC", r))
		}

		// 大 struct 值拷贝：目标必须与源共享内部指针，且数据完整。
		q := copyHuge(p)
		gcStress()
		if !hugeMatch(q, r) {
			ck(false, fmt.Sprintf("copied huge struct (round %d) corrupted after GC", r))
		}
		if q.n != p.n {
			ck(false, fmt.Sprintf("huge struct copy (round %d) did not share pointer field", r))
		}
		if in, ok := q.ifc.(*hNode); !ok || in != p.n {
			ck(false, fmt.Sprintf("huge struct copy (round %d) interface field corrupted", r))
		}

		resetHuge(q)
		gcStress()
		if !hugeZero(q) {
			ck(false, fmt.Sprintf("reset huge struct (round %d) not zeroed", r))
		}

		resetHuge(p)
		gcStress()
		if !hugeZero(p) {
			ck(false, fmt.Sprintf("reset source huge struct (round %d) not zeroed", r))
		}
		last = p
	}
	gcStress()
	runtime.KeepAlive(last)
	finish("l18_huge_struct_zero_assign")
}
