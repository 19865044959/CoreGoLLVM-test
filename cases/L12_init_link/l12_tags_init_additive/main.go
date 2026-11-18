// L12: build tag + init 的“叠加”语义。多个互相独立且同时为真的约束文件
// （linux、linux&&amd64、!windows）都必须贡献自己的包级变量初始化与 init；
// 未启用 tag 与 ignore 文件必须整体被排除。跨文件 init 顺序未定义，不断言。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
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

// expectedMarks mirrors the boolean build constraints using runtime.GOOS /
// runtime.GOARCH. The go tool applies exactly these predicates when selecting
// files, so this works on linux, windows, darwin and every architecture.
func expectedMarks() []string {
	var want []string
	if runtime.GOOS == "linux" {
		want = append(want, "linux.var", "linux.init")
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		want = append(want, "linux_amd64.var", "linux_amd64.init")
	}
	if runtime.GOOS != "windows" {
		want = append(want, "notwindows.var", "notwindows.init")
	}
	if runtime.GOOS == "windows" {
		want = append(want, "windows.var", "windows.init")
	}
	return want
}

func count(m []string, want string) int {
	n := 0
	for _, s := range m {
		if s == want {
			n++
		}
	}
	return n
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	got := append([]string(nil), marks...)
	want := expectedMarks()
	sortedGot := append([]string(nil), got...)
	sortedWant := append([]string(nil), want...)
	sort.Strings(sortedGot)
	sort.Strings(sortedWant)

	ck(len(got) == len(want), fmt.Sprintf("mark count %d, want %d (%v vs %v)", len(got), len(want), sortedGot, sortedWant))
	if len(sortedGot) == len(sortedWant) {
		for i := range sortedWant {
			if sortedGot[i] != sortedWant[i] {
				ck(false, fmt.Sprintf("mark set mismatch at %d: %s vs %s", i, sortedGot[i], sortedWant[i]))
				break
			}
		}
	}
	for _, m := range want {
		ck(count(got, m) == 1, "missing or duplicate mark "+m)
	}
	for _, m := range []string{"off.var", "off.init", "ignore.var", "ignore.init"} {
		ck(count(got, m) == 0, "excluded file contributed "+m)
	}
	if runtime.GOOS == "windows" {
		ck(count(got, "linux.var") == 0 && count(got, "linux_amd64.var") == 0, "linux files compiled on windows")
	}
	if runtime.GOOS == "linux" && runtime.GOARCH != "amd64" {
		ck(count(got, "linux_amd64.var") == 0, "linux&&amd64 file compiled on non-amd64")
	}

	// All package-level variable initialization must complete before any init
	// function, independent of the undefined cross-file ordering.
	lastVar := -1
	firstInit := len(got)
	for i, m := range got {
		if len(m) >= 4 && m[len(m)-4:] == ".var" {
			lastVar = i
		}
		if len(m) >= 5 && m[len(m)-5:] == ".init" {
			if i < firstInit {
				firstInit = i
			}
		}
	}
	ck(lastVar >= 0 && firstInit < len(got) && lastVar < firstInit,
		fmt.Sprintf("a var initializer ran after an init function (lastVar=%d firstInit=%d)", lastVar, firstInit))

	// Concurrent read + explicit GC; init state must not replay or change.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := 0
	if len(want) > 0 {
		start = int(seedVal % int64(len(want)))
	} else {
		ck(false, "no tagged file was selected on this platform")
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				m := marks
				if len(m) != len(want) {
					atomic.AddInt32(&bad, 1)
					return
				}
				if count(m, want[(start+w+i)%len(want)]) != 1 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 3; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent read of tagged init marks failed under GC")

	gcStress()
	after := append([]string(nil), marks...)
	ck(len(after) == len(want), "init mark count changed after GC")

	finish("l12_tags_init_additive")
}
