// L17: 尾调用形态 + 变参 accumulator。互相递归的偶数/奇数层交替经接口方法转发
// ...any；每层 defer 在栈回卷时 GC 并校验本层切片，最内层保存完整 accumulator。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	tag  string
	next *item
}

type box struct {
	p *item
	s string
}

type folder interface {
	fold(n int64, acc ...any) (int64, *item)
}

type impl struct {
	base int64
	tag  string
}

var (
	failed   int32
	uafSeed  int64 = 1
	finalAcc []any
	inst     folder = &impl{base: 7, tag: "impl"}
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func mkAccItem(v int64) *item {
	return &item{v: v, tag: "acc"}
}

func sumAcc(acc []any, where string) (int64, *item) {
	sum := int64(0)
	var tail *item
	for i, a := range acc {
		switch x := a.(type) {
		case nil:
		case *item:
			if x == nil || x.tag != "acc" {
				ck(false, fmt.Sprintf("%s: item[%d] corrupted seed=%d", where, i, uafSeed))
				continue
			}
			sum += x.v
			tail = x
		case box:
			if x.p == nil || x.p.tag != "acc" || x.s == "" {
				ck(false, fmt.Sprintf("%s: box[%d] corrupted seed=%d", where, i, uafSeed))
				continue
			}
			sum += x.p.v + int64(len(x.s))
			tail = x.p
		case string:
			sum += int64(len(x))
		default:
			ck(false, fmt.Sprintf("%s: unexpected type %T at %d seed=%d", where, a, i, uafSeed))
		}
	}
	return sum, tail
}

func baseFold(acc []any, where string) (int64, *item) {
	finalAcc = acc
	return sumAcc(acc, where)
}

//go:noinline
func (f *impl) fold(n int64, acc ...any) (int64, *item) {
	gcStress()
	if f.tag != "impl" {
		ck(false, fmt.Sprintf("impl.fold: receiver corrupted seed=%d", uafSeed))
	}
	if n <= 0 {
		return baseFold(acc, "impl-base")
	}
	return oddFold(n-1, append(acc, mkAccItem(f.base+n))...)
}

//go:noinline
func evenFold(n int64, acc ...any) (int64, *item) {
	defer func() {
		gcStress()
		if _, tail := sumAcc(acc, "even-defer"); tail != nil && tail.tag != "acc" {
			ck(false, fmt.Sprintf("evenFold defer: tail corrupted seed=%d", uafSeed))
		}
	}()
	if n <= 0 {
		return baseFold(acc, "even-base")
	}
	if n%2 == 0 {
		// 尾位置：经接口分发进入下一层。
		return inst.fold(n-1, append(acc, box{p: mkAccItem(n + 1000), s: "b"})...)
	}
	return oddFold(n-1, append(acc, "s")...)
}

//go:noinline
func oddFold(n int64, acc ...any) (int64, *item) {
	defer func() {
		gcStress()
		if _, tail := sumAcc(acc, "odd-defer"); tail != nil && tail.tag != "acc" {
			ck(false, fmt.Sprintf("oddFold defer: tail corrupted seed=%d", uafSeed))
		}
	}()
	if n <= 0 {
		return baseFold(acc, "odd-base")
	}
	return evenFold(n-1, append(acc, mkAccItem(n))...)
}

func main() {
	uafSeed = seedFromEnv()
	for r := 0; r < 6; r++ {
		n := int64(16 + r*4 + int(uafSeed%3))
		if n > 40 {
			n = 40
		}
		start := []any{mkAccItem(int64(r) + 1), "seed", nil}
		finalAcc = nil

		sum, tail := evenFold(n, start...)
		gcStress()
		if finalAcc == nil {
			ck(false, fmt.Sprintf("round %d: final accumulator not recorded seed=%d", r, uafSeed))
			continue
		}
		if len(finalAcc) <= len(start) {
			ck(false, fmt.Sprintf("round %d: accumulator did not grow len=%d seed=%d", r, len(finalAcc), uafSeed))
		}
		gcStress()
		gotSum, gotTail := sumAcc(finalAcc, "caller-final")
		if gotSum != sum {
			ck(false, fmt.Sprintf("round %d: sum across GC=%d want=%d seed=%d", r, gotSum, sum, uafSeed))
		}
		if gotTail != tail {
			ck(false, fmt.Sprintf("round %d: tail across GC mismatched seed=%d", r, uafSeed))
		}
		// 调用后 start 切片仍应有效。
		startSum, startTail := sumAcc(start, "caller-start")
		if startSum != int64(r)+1+int64(len("seed")) || startTail != start[0].(*item) {
			ck(false, fmt.Sprintf("round %d: caller start slice corrupted seed=%d", r, uafSeed))
		}
		gcStress()
	}

	finish("l17_tailcall_variadic_accum")
}
