// L12: 大 init 表。init() 中构建大规模函数分派表：每项含函数值、名字、
// 指针 payload 与 next 链；同时建立名字索引。主程序全量校验、调用每个函数
// 值、显式 GC 后复核，并在并发只读期间穿插 GC。TORTURE_ROUNDS 放大表规模。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const baseTable = 8192

type payload struct {
	id   int
	name string
	tag  uint32
}

type entry struct {
	id   int
	name string
	fn   func(int) int
	p    *payload
	next *entry
}

func op0(x int) int { return x + 3 }
func op1(x int) int { return x*2 + 1 }
func op2(x int) int { return x ^ 0x5a }
func op3(x int) int { return x*x%1000 + 7 }
func op4(x int) int { return (x + 11) / 3 }
func op5(x int) int { return -(x % 13) }
func op6(x int) int { return x << 1 }
func op7(x int) int { return x - 4 }

var fnTable = [8]func(int) int{op0, op1, op2, op3, op4, op5, op6, op7}

var (
	failed   int32
	gcSink   [][]byte
	seedVal  int64
	seedStr  string
	dispatch []*entry
	byName   map[string]*entry
	head     *entry
)

func wantOp(idx, x int) int {
	switch idx & 7 {
	case 0:
		return x + 3
	case 1:
		return x*2 + 1
	case 2:
		return x ^ 0x5a
	case 3:
		return x*x%1000 + 7
	case 4:
		return (x + 11) / 3
	case 5:
		return -(x % 13)
	case 6:
		return x << 1
	default:
		return x - 4
	}
}

func tortureRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 8 {
		v = 8
	}
	return v
}

func init() {
	n := baseTable * tortureRounds()
	dispatch = make([]*entry, n)
	byName = make(map[string]*entry, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("op_%05d", i)
		e := &entry{
			id:   i,
			name: name,
			fn:   fnTable[i%8],
			p:    &payload{id: i, name: name, tag: uint32(i * 7)},
		}
		dispatch[i] = e
		byName[name] = e
		if i > 0 {
			dispatch[i-1].next = e
		}
	}
	head = dispatch[0]
}

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
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func verifyAll() {
	n := len(dispatch)
	for i := 0; i < n; i++ {
		e := dispatch[i]
		name := fmt.Sprintf("op_%05d", i)
		ck(e != nil, "entry non-nil")
		if e == nil {
			return
		}
		ck(e.id == i, "entry id")
		ck(e.name == name, "entry name")
		ck(e.p != nil && e.p.id == i && e.p.name == name && e.p.tag == uint32(i*7),
			"payload fields")
		ck(byName[name] == e, "name map points to same entry")
		arg := i*3 + 5
		ck(e.fn != nil && e.fn(arg) == wantOp(i%8, arg), "dispatch result")
		if i+1 < n {
			ck(e.next == dispatch[i+1], "next chain")
		} else {
			ck(e.next == nil, "tail next nil")
		}
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	n := len(dispatch)
	ck(n == baseTable*tortureRounds(), "init table size follows TORTURE_ROUNDS")
	ck(len(byName) == n, "init name index size")
	ck(head == dispatch[0], "init head pointer")

	// Full structural check plus every function-value call.
	var sum, wantSum int64
	for i := 0; i < n; i++ {
		arg := i*3 + 5
		sum += int64(dispatch[i].fn(arg))
		wantSum += int64(wantOp(i%8, arg))
	}
	ck(sum == wantSum, "every dispatch result matches expected operator")
	verifyAll()

	// Explicit GC rounds with fresh garbage preserving the whole table graph.
	for r := 0; r < 3; r++ {
		for j := 0; j < 64; j++ {
			b := make([]byte, 16384)
			b[j] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
		ck(head == dispatch[0], "head identity after GC")
		ck(byName[dispatch[n/2].name] == dispatch[n/2], "middle entry identity after GC")
	}
	verifyAll()

	// Concurrent read-only dispatch while GC runs.
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			off := (w + int(seedVal)) % 8
			for i := off; i < n; i += 8 {
				arg := i*3 + 5
				if dispatch[i].fn(arg) != wantOp(i%8, arg) || dispatch[i].p.id != i {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for i := 0; i < 6; i++ {
		gcSink = append(gcSink, make([]byte, 32768))
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent dispatch under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(len(dispatch) == n && len(byName) == n, "table size survives GC")
	verifyAll()

	finish("l12_big_init_table")
}
