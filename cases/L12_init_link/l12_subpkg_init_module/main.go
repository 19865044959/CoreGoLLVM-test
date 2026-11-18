// L12: 子包 init（module 多包）。inner -> lib 是线性依赖，other 是 inner 的
// 第二个导入者。只断言规范保证的性质：依赖包完整初始化早于导入者、每个包
// init 恰好一次、导入者包级变量能看到被导入包 init 的效果。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"

	"l12_subpkg_init_module/inner"
	"l12_subpkg_init_module/lib"
	"l12_subpkg_init_module/other"
	"l12_subpkg_init_module/sink"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	// mainSnapshot runs after lib (and therefore inner) initialized.
	mainSnapshot = lib.Snapshot()
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

	// Subpackage values and readiness.
	ck(inner.Value == 7, "inner package value")
	ck(inner.Ready, "inner init completed")
	ck(inner.InitCount() == 1, "inner init runs exactly once")
	ck(lib.Value == 71, "lib value computes from inner")
	ck(lib.Ready && lib.InnerReady(), "lib init sees completed inner")
	ck(other.Value == 8, "second importer value")
	ck(other.Ready, "second importer init ready")
	ck(other.SeenInner() == 1, "second importer sees inner init count 1")

	// The linear inner -> lib order is guaranteed. other may run before or
	// after lib, so it contributes no marks and no order assertion.
	marks := sink.Marks()
	ck(len(marks) == 5, "mark count after all init")
	want := []string{"inner.var", "inner.init", "lib.var", "lib.init", "main.init"}
	ck(sameStrings(marks, want), "subpackage init order is dependency order")
	ck(len(mainSnapshot) == 4, "main var initializer sees four subpackage marks")
	ck(sameStrings(mainSnapshot, want[:4]), "main snapshot matches subpackage init prefix")

	// Concurrent reads of subpackage init state plus explicit GC.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := int(seedVal % 3)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if inner.InitCount() != 1 || lib.Value != 71 || other.SeenInner() != 1 {
					atomic.AddInt32(&bad, 1)
					return
				}
				if len(sink.Marks()) != 5 {
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
	ck(atomic.LoadInt32(&bad) == 0, "concurrent subpackage state reads under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(inner.InitCount() == 1, "GC does not replay inner init")
	ck(lib.Value == 71 && lib.Ready, "lib state survives GC")
	ck(sameStrings(sink.Marks(), want), "mark trace unchanged after GC")
	ck(sameStrings(mainSnapshot, want[:4]), "main snapshot unchanged after GC")

	finish("l12_subpkg_init_module")
}
