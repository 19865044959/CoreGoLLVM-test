// L12: 注册表重复注册检测与稳定顺序（启动内置项 + 并发注册 + 冻结）。
package main

import (
	"errors"
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

func namesEqual(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func checkEntries(r *registry, want map[string]int) {
	for name, value := range want {
		e, ok := r.Lookup(name)
		if !ok || e == nil {
			ck(false, "missing entry "+name)
			continue
		}
		ck(e.name == name && e.value == value, "entry value "+name)
		good := e.impl != nil && e.impl.tag == name+"#v1" && len(e.impl.data) == 16
		if good {
			for i := 0; i < 16; i++ {
				if e.impl.data[i] != byte(value+i) {
					good = false
				}
			}
		}
		ck(good, "entry impl data "+name)
	}
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	r := defaultRegistry
	ck(builtinInitErr == nil, "builtin init had no error")
	ck(r.Len() == 3, "three builtin entries")
	ck(namesEqual(r.Names(), "gob", "json", "text"), "builtin names sorted")
	ck(r.Dump() == "gob=1,json=2,text=3", "builtin dump stable")

	// 重名检测：值不得被覆盖。
	err := r.Register("json", 99)
	ck(errors.Is(err, errDuplicate), "duplicate json detected")
	ck(r.Len() == 3, "duplicate does not grow registry")
	e, _ := r.Lookup("json")
	ck(e != nil && e.value == 2, "duplicate does not overwrite value")

	ck(r.Register("yaml", 4) == nil, "register yaml")
	err = r.Register("yaml", 40)
	ck(errors.Is(err, errDuplicate), "duplicate yaml detected")
	ck(r.Len() == 4, "yaml counted once")
	ck(r.Dump() == "gob=1,json=2,text=3,yaml=4", "dump after yaml")

	// 并发重复注册同一个名字：恰好一个成功，其余报重复。
	const workers = 24
	var wg sync.WaitGroup
	var wins, dups int32
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := r.Register("zstd", 7)
			switch {
			case err == nil:
				atomic.AddInt32(&wins, 1)
			case errors.Is(err, errDuplicate):
				atomic.AddInt32(&dups, 1)
			default:
				ck(false, "unexpected concurrent register error: "+fmt.Sprint(err))
			}
		}()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&wins) == 1, "exactly one concurrent registration wins")
	ck(atomic.LoadInt32(&dups) == workers-1, "all losers see duplicate error")
	ck(r.Len() == 5, "registry length after concurrent register")
	ck(r.Dump() == "gob=1,json=2,text=3,yaml=4,zstd=7", "dump after concurrent register")

	// 冻结后拒绝新注册，顺序与内容保持不变。
	r.Freeze()
	err = r.Register("xml", 8)
	ck(errors.Is(err, errFrozen), "frozen registry rejects register")
	ck(r.Len() == 5, "frozen registry unchanged")
	ck(r.Dump() == "gob=1,json=2,text=3,yaml=4,zstd=7", "dump after freeze")

	// 并发只读 + GC。
	want := map[string]int{"gob": 1, "json": 2, "text": 3, "yaml": 4, "zstd": 7}
	var rwg sync.WaitGroup
	var bad int32
	for w := 0; w < 8; w++ {
		rwg.Add(1)
		go func(w int) {
			defer rwg.Done()
			start := int((seedVal + int64(w)) % 5)
			for i := 0; i < 300; i++ {
				names := r.Names()
				if len(names) != 5 {
					atomic.AddInt32(&bad, 1)
					return
				}
				name := names[(start+i)%5]
				e, ok := r.Lookup(name)
				if !ok || e == nil || e.value != want[name] {
					atomic.AddInt32(&bad, 1)
					return
				}
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
	rwg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent reads under GC")

	gcStress()
	checkEntries(r, want)
	ck(r.Dump() == "gob=1,json=2,text=3,yaml=4,zstd=7", "dump intact after GC")
	ck(namesEqual(r.Names(), "gob", "json", "text", "yaml", "zstd"), "names intact after GC")

	finish("l12_registry_duplicates")
}
