// L12: `var _ = register(...)` 在包级变量初始化阶段产生注册副作用；
// 同包多文件模拟插件注册，init 运行时注册表必须已完整。
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

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	// 所有 `var _ = register(...)` 都在 init 之前完成。
	ck(registerCallsAtInit == 3, "three blank-var registrations visible in init")
	ck(atomic.LoadInt32(&registerCalls) == 3, "register called exactly three times")
	ck(registrySizeAtInit == 3, "registry has 3 entries when init runs")

	names := codecNames()
	ck(len(names) == 3, "registry names length")
	ck(names[0] == "alpha" && names[1] == "beta" && names[2] == "gamma", "sorted registry names")

	want := map[string]int{"alpha": 3, "beta": 5, "gamma": 7}
	for _, n := range names {
		c := registry.m[n]
		if c == nil {
			ck(false, "missing codec "+n)
			continue
		}
		ck(c.name == n && c.factor == want[n], "codec factor for "+n)
		good := c.mark != nil
		if c.mark != nil {
			for i := 0; i < 16; i++ {
				if c.mark[i] != byte(want[n]+i) {
					good = false
				}
			}
		}
		ck(good, "codec mark bytes for "+n)
	}
	ck(total(10) == 150, "total through registered codecs")
	ck(markChecksum() == 600, "mark checksum across registry")

	// 并发只读 + GC：注册表在 init 后不可变。
	const workers = 8
	const iters = 400
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			start := int((seedVal + int64(w)) % 3)
			for i := 0; i < iters; i++ {
				n := names[(start+i)%3]
				c := registry.m[n]
				if c == nil || c.name != n || c.factor != want[n] {
					atomic.AddInt32(&bad, 1)
					return
				}
				if total(2) != 30 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}

	// 主 goroutine 在并发读期间制造垃圾并触发 GC。
	for i := 0; i < 4; i++ {
		for j := 0; j < 8; j++ {
			b := make([]byte, 8192)
			b[j] = byte(i)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent lookups under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(atomic.LoadInt32(&registerCalls) == 3, "no re-registration after GC")
	ck(len(codecNames()) == 3 && total(1) == 15, "registry intact after GC")
	for _, n := range codecNames() {
		c := registry.m[n]
		ck(c != nil && c.mark != nil && c.mark[15] == byte(want[n]+15), "codec pointer and mark survive GC: "+n)
	}

	finish("l12_blank_import_register")
}
