// L05: 容量增长压力：用 slices.Grow/Insert/Delete/Clone/Concat 对指针元素
// 切片做多代扩容、截断、插入、删除与复制；粗粒度垃圾分配 + 显式 GC，
// 并行的预期 id 序列同步执行相同操作，最终逐项校验指针目标与顺序。
package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
)

const (
	capGens = 90
	capPer  = 193
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
	for i := 0; i < 24; i++ {
		b := make([]byte, 32<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

type capNode struct {
	id   int
	name string
	data []byte
}

func capDataLen(id int) int {
	return 12 + int(uint64(id)%53)
}

func capDataByte(id, off int, seed uint64) byte {
	return byte((seed*7 + uint64(id)*11 + uint64(off)*3) % 251)
}

func makeCapNode(id int, seed uint64) *capNode {
	n := capDataLen(id)
	b := make([]byte, n)
	for i := range b {
		b[i] = capDataByte(id, i, seed)
	}
	return &capNode{id: id, name: fmt.Sprintf("c-%d", id), data: b}
}

func verifyCap(tag string, s []*capNode, want []int, seed uint64) {
	ckf(len(s) == len(want), "%s len=%d want=%d", tag, len(s), len(want))
	if len(s) != len(want) {
		return
	}
	for i := range s {
		n := s[i]
		if n == nil {
			ckf(false, "%s i=%d nil node", tag, i)
			continue
		}
		ckf(n.id == want[i], "%s i=%d id=%d want=%d", tag, i, n.id, want[i])
		ckf(n.name == fmt.Sprintf("c-%d", want[i]), "%s i=%d name=%q", tag, i, n.name)
		if len(n.data) != capDataLen(want[i]) {
			ckf(false, "%s i=%d data len=%d want=%d", tag, i, len(n.data), capDataLen(want[i]))
			continue
		}
		for j := 0; j < len(n.data); j += 7 {
			if n.data[j] != capDataByte(want[i], j, seed) {
				ckf(false, "%s i=%d data byte at=%d", tag, i, j)
				break
			}
		}
	}
}

func main() {
	seed := loadSeed()
	s := make([]*capNode, 0, 1)
	want := make([]int, 0, 1)
	pins := make([]*capNode, 0, 5)

	for gen := 0; gen < capGens; gen++ {
		if gen%23 == 5 && len(s) > 64 {
			cut := len(s) / 2
			s = s[:cut]
			want = want[:cut]
		}

		beforeLen := len(s)
		s = slices.Grow(s, capPer)
		ckf(len(s) == beforeLen, "gen=%d Grow changed len=%d want=%d", gen, len(s), beforeLen)
		ckf(cap(s) >= len(s)+capPer, "gen=%d Grow cap=%d < len+%d", gen, cap(s), capPer)

		for i := 0; i < capPer; i++ {
			id := gen*100000 + i
			n := makeCapNode(id, seed)
			s = append(s, n)
			want = append(want, id)
			if gen == 0 && i < len(pins)+5 && len(pins) < 5 {
				pins = append(pins, n)
			}
		}

		if gen%7 == 0 {
			pos := len(s) / 3
			ins := make([]*capNode, 5)
			ids := make([]int, 5)
			for j := 0; j < 5; j++ {
				ids[j] = gen*100000 + 50000 + j
				ins[j] = makeCapNode(ids[j], seed)
			}
			s = slices.Insert(s, pos, ins...)
			want = slices.Insert(want, pos, ids...)
		}

		if gen%11 == 0 && len(s) > 80 {
			from := len(s) / 5
			to := from + 7
			s = slices.Delete(s, from, to)
			want = slices.Delete(want, from, to)
		}

		if gen%10 == 0 {
			verifyCap(fmt.Sprintf("gen=%d before-GC", gen), s, want, seed)
			gcStress()
			verifyCap(fmt.Sprintf("gen=%d after-GC", gen), s, want, seed)
		}
	}

	gcStress()
	verifyCap("final", s, want, seed)
	for i := range pins {
		ckf(pins[i] != nil && pins[i].id == i, "pinned node %d corrupted", i)
	}

	// Clone/Concat 各复制指针元素；GC 后副本和原切片都必须完整。
	cl := slices.Clone(s)
	head := slices.Clone(s[:10])
	tail := slices.Clone(s[len(s)-10:])
	cc := slices.Concat(head, tail)
	gcStress()
	verifyCap("clone", cl, want, seed)
	verifyCap("concat", cc, slices.Concat(want[:10], want[len(want)-10:]), seed)
	for i := range cl {
		if cl[i] != s[i] {
			ckf(false, "clone i=%d lost pointer identity", i)
			break
		}
	}

	// 释放大部分内容，仅保留 pin，再次 GC 后校验 pin。
	cl = nil
	cc = nil
	head = nil
	tail = nil
	s = nil
	want = nil
	gcStress()
	for i := range pins {
		ckf(pins[i] != nil && pins[i].name == fmt.Sprintf("c-%d", i), "pinned node %d corrupted after drop", i)
		for j := 0; j < len(pins[i].data); j += 9 {
			if pins[i].data[j] != capDataByte(i, j, seed) {
				ckf(false, "pinned node %d data corrupted", i)
				break
			}
		}
	}
	runtime.KeepAlive(seed)

	finish("l05_cap_growth_pressure", seed)
}
