// L12: 构建标签与 init 的组合。同一包内按 //go:build 选择实现文件：
//   - linux / !linux 二选一，被选中的文件在 init 中记录标记；
//   - 自定义标签 l12special 未启用，该文件必须整文件排除（含其 init）；
//   - 标签 ignore 永不参与构建。
//
// 只断言当前 GOOS 下确定的文件选择结果，不打印/不比较跨文件 init 顺序。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed    int32
	gcSink    [][]byte
	seedVal   int64
	seedStr   string
	initMarks []string
)

func tagAdd(m string) { initMarks = append(initMarks, m) }

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

func containsMark(m string) bool {
	for _, v := range initMarks {
		if v == m {
			return true
		}
	}
	return false
}

func checkTagSelection() {
	// Exactly one GOOS-tagged file may contribute its init.
	ck(len(initMarks) == 1, "exactly one GOOS-tagged init ran")
	ck(!containsMark("special.init"), "custom-tag file init excluded")
	ck(!containsMark("ignore.init"), "ignore-tag file init excluded")

	if runtime.GOOS == "linux" {
		ck(containsMark("linux.init"), "linux-tagged file selected")
		ck(!containsMark("other.init"), "non-linux file excluded")
		ck(taggedImpl() == "linux", "linux-tagged implementation linked")
	} else {
		ck(containsMark("other.init"), "non-linux-tagged file selected")
		ck(!containsMark("linux.init"), "linux file excluded")
		ck(taggedImpl() == "other", "non-linux implementation linked")
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	checkTagSelection()

	// Concurrent reads of the tag-selected implementation and the init marks,
	// with explicit GC in the middle.
	var wg sync.WaitGroup
	var bad int32
	start := int(seedVal % 2)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			want := "linux"
			if runtime.GOOS != "linux" {
				want = "other"
			}
			for i := 0; i < 200; i++ {
				if taggedImpl() != want || len(initMarks) != 1 {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = (start + w + i) % 2
			}
		}(w)
	}
	for i := 0; i < 8; i++ {
		gcSink = append(gcSink, make([]byte, 8192))
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "tag-selected state stable under GC")
	gcSink = gcSink[:0]

	gcStress()
	checkTagSelection()
	ck(len(initMarks) == 1, "init not replayed after GC")

	finish("l12_tags_init")
}
