// L11: 接口字段去虚拟化 × 数组值拷贝——含接口字段的结构体数组与接口数组
// 整体按值传递/拷贝后仍按原动态类型分派；拷贝中的非接口字段互不影响，
// 接口字段重绑定不回流；仅由数组保活的指针实现跨显式 GC 仍完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ifOp interface {
	Apply(int64) int64
	Name() string
}

type ifPlus struct{ k int64 }

func (o ifPlus) Apply(x int64) int64 { return x + o.k }
func (o ifPlus) Name() string        { return "plus" }

type ifTimes struct{ k int64 }

func (o *ifTimes) Apply(x int64) int64 { return x * o.k }
func (o *ifTimes) Name() string        { return "times" }

type ifNilSafe struct{ k int64 }

func (o *ifNilSafe) Apply(x int64) int64 {
	if o == nil {
		return -55
	}
	return x + o.k
}
func (o *ifNilSafe) Name() string {
	if o == nil {
		return "nil-safe"
	}
	return "safe"
}

type ifBase struct{ k int64 }

func (b ifBase) Apply(x int64) int64 { return x - b.k }
func (b ifBase) Name() string        { return "base" }

type ifEmbed struct{ ifBase }

type ifCell struct {
	op  ifOp
	tag string
	n   int64
}

type ifOuter struct {
	inner ifCell
	label string
}

type ifRound struct {
	cells [12]ifCell
	ops   [12]ifOp
	want  [12]int64
	x     int64
}

const ifN = 12

var (
	failed   int32
	gcSink   [][]byte
	seed     uint64 = 1
	ifRounds []ifRound
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
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

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	if n > 50 {
		n = 50
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func ifMake(i int, k int64) ifOp {
	switch i % 4 {
	case 0:
		return ifPlus{k: k}
	case 1:
		return &ifTimes{k: k}
	case 2:
		return &ifNilSafe{k: k}
	default:
		return ifEmbed{ifBase{k: k}}
	}
}

func ifExpected(op ifOp, x int64) int64 {
	switch v := op.(type) {
	case ifPlus:
		return v.Apply(x)
	case *ifTimes:
		return v.Apply(x)
	case ifEmbed:
		return v.Apply(x)
	case *ifNilSafe:
		return v.Apply(x)
	}
	return -1
}

func ifSumCells(cells [ifN]ifCell, x int64) (int64, int) {
	var sum int64
	count := 0
	for i := range cells {
		if cells[i].op != nil {
			sum += cells[i].op.Apply(x)
			count++
		}
	}
	return sum, count
}

func ifSumOps(ops [ifN]ifOp, x int64) int64 {
	var sum int64
	for i := range ops {
		if ops[i] != nil {
			sum += ops[i].Apply(x)
		}
	}
	return sum
}

func expectPanic(name string, fn func()) {
	defer func() {
		if recover() == nil {
			ck(false, name+": expected panic")
		}
	}()
	fn()
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	rounds := 4 * tortureMultiplier()

	for r := 0; r < rounds; r++ {
		x := int64(seed%23) - 11 + int64(r)
		var rec ifRound
		rec.x = x
		for i := 0; i < ifN; i++ {
			k := int64((i*3 + r) % 17)
			op := ifMake(i, k)
			rec.cells[i] = ifCell{op: op, tag: "c" + strconv.Itoa(i), n: int64(i)}
			rec.ops[i] = op
			rec.want[i] = ifExpected(op, x)
		}
		rec.cells[ifN-1].op = (*ifNilSafe)(nil)
		rec.ops[ifN-1] = rec.cells[ifN-1].op
		rec.want[ifN-1] = -55

		wantSum := int64(0)
		for i := range rec.want {
			wantSum += rec.want[i]
		}
		gotSum, count := ifSumCells(rec.cells, x)
		ck(gotSum == wantSum && count == ifN, "array interface field dispatch")
		ck(ifSumOps(rec.ops, x) == wantSum, "interface array dispatch")

		cp := rec.cells
		cp[0].tag = "changed"
		cp[0].n = 999
		cp[0].op = ifPlus{k: 1000}
		ck(rec.cells[0].tag == "c0" && rec.cells[0].n == 0, "array copy isolates value fields")
		ck(rec.cells[0].op.Apply(x) == rec.want[0], "array copy preserves original interface")
		ck(cp[0].op.Apply(x) == x+1000, "array copy rebinding is local")

		opsCp := rec.ops
		opsOK := true
		for i := 0; i < ifN; i++ {
			if a, ok := rec.ops[i].(*ifTimes); ok {
				b, ok2 := opsCp[i].(*ifTimes)
				if !ok2 || a != b {
					opsOK = false
				}
			}
		}
		ck(opsOK, "interface array copy keeps pointer implementation identity")
		ck(ifSumOps(opsCp, x) == wantSum, "interface array copy dispatch")

		var cell ifCell
		rebinds := []struct {
			op   ifOp
			want int64
			name string
		}{
			{ifPlus{k: 2}, x + 2, "plus"},
			{&ifTimes{k: 3}, x * 3, "times"},
			{(*ifNilSafe)(nil), -55, "nil-safe"},
			{ifEmbed{ifBase{k: 4}}, x - 4, "base"},
		}
		for _, rb := range rebinds {
			cell.op = rb.op
			ck(cell.op.Apply(x) == rb.want && cell.op.Name() == rb.name, "interface field rebinding")
		}

		outer := ifOuter{inner: rec.cells[3], label: "outer"}
		outCp := outer
		outCp.inner.op = &ifTimes{k: 5}
		outCp.inner.tag = "inner-copy"
		ck(outer.inner.op.Apply(x) == rec.want[3], "nested copy preserves original dispatch")
		ck(outCp.inner.op.Apply(x) == x*5, "nested copy rebinding dispatch")
		ck(outer.inner.tag == "c3" && outCp.inner.tag == "inner-copy", "nested copy isolates tag")

		var nilCell ifCell
		ck(nilCell.op == nil, "zero interface field is nil")
		expectPanic("nil interface field apply", func() { _ = nilCell.op.Apply(x) })

		ifRounds = append(ifRounds, rec)
		gcStress()

		gotSum, count = ifSumCells(rec.cells, x)
		ck(gotSum == wantSum && count == ifN, "array dispatch after GC")
		ck(ifSumOps(rec.ops, x) == wantSum, "interface array after GC")
	}

	gcStress()
	for ri := range ifRounds {
		rec := &ifRounds[ri]
		bad := false
		for i := 0; i < ifN; i++ {
			if rec.cells[i].op == nil || rec.cells[i].op.Apply(rec.x) != rec.want[i] {
				bad = true
				break
			}
			if rec.ops[i] == nil || rec.ops[i].Apply(rec.x) != rec.want[i] {
				bad = true
				break
			}
		}
		if bad {
			ck(false, "retained interface arrays after final GC")
			break
		}
	}

	finish("l11_devirt_iface_array")
}
