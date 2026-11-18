// L12: 跨包 init 顺序（module）。线性链 c -> b -> a 的顺序由规范保证；
// diamond 叶 leaf 必须早于 dl/dr；dl 与 dr 是兄弟包，相对顺序未定义，不做断言。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"l12_crosspkg_init_chain/a"
	"l12_crosspkg_init_chain/dl"
	"l12_crosspkg_init_chain/dr"
	"l12_crosspkg_init_chain/leaf"
	"l12_crosspkg_init_chain/sink"
	"l12_crosspkg_init_chain/top"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	mainSnapshot = sink.Marks()
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

func init() {
	sink.Add("main.init")
}

func countMark(m string) int {
	n := 0
	for _, v := range sink.Marks() {
		if v == m {
			n++
		}
	}
	return n
}

// checkBlock asserts that a package's var mark is immediately followed by its
// init mark: no other package may interleave inside one package's init.
func checkBlock(pkg string) {
	v := sink.Index(pkg + ".var")
	i := sink.Index(pkg + ".init")
	ck(v >= 0 && i == v+1, "block "+pkg+": var then init")
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	marks := sink.Marks()
	ck(len(marks) == 15, "trace length after main.init")
	ck(len(mainSnapshot) == 14, "main var snapshot sees all imported packages")

	pkgs := []string{"c", "b", "a", "leaf", "dl", "dr", "top"}
	for _, p := range pkgs {
		ck(countMark(p+".var") == 1, "one "+p+".var mark")
		ck(countMark(p+".init") == 1, "one "+p+".init mark")
		checkBlock(p)
	}
	ck(countMark("main.init") == 1, "one main.init mark")

	// Linear chain c -> b -> a is strictly ordered.
	ck(sink.Index("c.init") < sink.Index("b.var"), "c completes before b")
	ck(sink.Index("b.init") < sink.Index("a.var"), "b completes before a")
	ck(sink.Index("c.var") < sink.Index("c.init"), "c var before init")
	ck(sink.Index("b.var") < sink.Index("b.init"), "b var before init")
	ck(sink.Index("a.var") < sink.Index("a.init"), "a var before init")

	// Diamond: leaf before both branches; both branches before top.
	ck(sink.Index("leaf.init") < sink.Index("dl.var"), "leaf before dl")
	ck(sink.Index("leaf.init") < sink.Index("dr.var"), "leaf before dr")
	ck(sink.Index("a.init") < sink.Index("top.var"), "a before top")
	ck(sink.Index("dl.init") < sink.Index("top.var"), "dl before top")
	ck(sink.Index("dr.init") < sink.Index("top.var"), "dr before top")
	ck(sink.Index("leaf.init") < sink.Index("top.var"), "leaf before top")
	// dl vs dr relative order is deliberately not asserted.
	ck(marks[14] == "main.init", "main.init is last")
	ck(sameStrings(mainSnapshot, marks[:14]), "main var snapshot matches imported trace")

	// Values computed through the chain and diamond.
	ck(a.V == 22 && a.Ready, "chain value a")
	ck(dl.V == 10 && dl.Ready && dr.V == 15 && dr.Ready, "diamond branch values")
	ck(leaf.V == 5 && leaf.Ready, "leaf value")
	ck(top.V == 250 && top.Ready && top.Seen, "top sees all dependencies and computes value")

	// Concurrent reads + GC.
	var wg sync.WaitGroup
	var bad int32
	start := int(seedVal % 3)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if top.V != 250 || a.V != 22 || dl.V+dr.V != 25 || leaf.V != 5 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if len(sink.Marks()) != 15 {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = (start + w + i) % 3
			}
		}(w)
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 8; j++ {
			b := make([]byte, 8192)
			b[j] = byte(i)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent reads of init state under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(len(sink.Marks()) == 15, "init trace not replayed after GC")
	ck(sameStrings(sink.Marks(), marks), "trace identical after GC")
	ck(top.V == 250 && a.V == 22, "computed values survive GC")

	finish("l12_crosspkg_init_chain")
}
