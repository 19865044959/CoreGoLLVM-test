// L06: 成功/失败类型断言、nil 接口、带 ok 断言循环，跨 GC 结果一致。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type reader interface{ Read() int64 }
type closer interface{ Close() int64 }

type file struct {
	V   int64
	Tag *string
}

func (f *file) Read() int64  { return f.V + int64(len(*f.Tag)) }
func (f *file) Close() int64 { return -f.V }

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
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
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()

	var nilAny any
	ck(nilAny == nil, "nil interface is nil")
	v0, ok0 := nilAny.(int)
	ck(!ok0 && v0 == 0, "assert nil interface returns zero,false")

	var typedNil *file
	var boxedTypedNil any = typedNil
	ck(boxedTypedNil != nil, "boxed typed nil is non-nil interface")
	tn, ok := boxedTypedNil.(*file)
	ck(ok && tn == nil, "typed nil asserts to nil pointer")
	rNil, ok := boxedTypedNil.(reader)
	ck(ok && rNil != nil, "typed nil implements reader without call")
	backNil, ok := rNil.(*file)
	ck(ok && backNil == nil, "reader back to nil pointer")

	var nilReader reader
	var boxedNilIface any = nilReader
	ck(boxedNilIface == nil, "nil interface value boxes to nil")

	tag := "abc"
	f := &file{V: 7, Tag: &tag}
	var a any = f
	gotInt, ok := a.(int)
	ck(!ok && gotInt == 0, "failed assertion returns zero,false")
	rd, ok := a.(reader)
	ck(ok && rd.Read() == 10, "assert to reader")
	cl, ok := rd.(closer)
	ck(ok && cl.Close() == -7, "interface-to-interface assertion")
	_, ok = a.(closer)
	ck(ok, "assert concrete value to closer")

	// 非 ok 形式失败断言必须 panic，并可被 recover。
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_ = a.(string)
	}()
	ck(panicked, "failed assertion panics")

	const rounds = 80
	var matched, anyMatched int
	for r := 0; r < rounds; r++ {
		tag := fmt.Sprintf("t%d", r)
		p := &file{V: int64(r + 1), Tag: &tag}
		vals := []any{int(r), "s", p, nil, int64(r), true, []int{r}, map[string]int{"v": r}}
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)))
		rnd.Shuffle(len(vals), func(i, j int) { vals[i], vals[j] = vals[j], vals[i] })

		for _, x := range vals {
			if iv, ok := x.(int); ok {
				matched++
				_ = iv
			}
			if sv, ok := x.(string); ok {
				matched++
				_ = sv
			}
			if fp, ok := x.(*file); ok {
				matched++
				if fp.V <= 0 || fp.Tag == nil {
					ck(false, "asserted file pointer corrupted")
				}
			}
			if _, ok := x.(int64); ok {
				matched++
			}
			if _, ok := x.(bool); ok {
				matched++
			}
			if _, ok := x.([]int); ok {
				matched++
			}
			if _, ok := x.(map[string]int); ok {
				matched++
			}
			if rv, ok := x.(reader); ok {
				if rv.Read() != fpV(r)+int64(len(fmt.Sprintf("t%d", r))) {
					ck(false, "reader assertion result corrupted")
				}
				matched++
			}
			if _, ok := x.(closer); ok {
				matched++
			}
			if _, ok := x.(any); ok {
				anyMatched++
			}
		}
		if r%16 == 0 {
			gcStress()
		}
	}
	// 每轮：7 个具体类型断言 + reader/closer 各命中 *file 一次。
	ck(matched == rounds*9, "loop assertion matched count")
	ck(anyMatched == rounds*7, "assert to any matches non-nil only")
	ck(f.Read() == 10 && f.Tag != nil && *f.Tag == "abc", "original pointer survives all GC")
	finish("l06_type_assert_loop")
}

func fpV(r int) int64 { return int64(r + 1) }
