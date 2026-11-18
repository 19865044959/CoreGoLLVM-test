// L13: map clear 复用：clear 后长度/遍历/查找归零，同表重填，nil/空 map 与 delete 循环对照。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type item struct {
	id   int64
	name string
	salt int64
	pad  []byte
}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	if v > 200 {
		return 200
	}
	return v
}

func newItem(id, salt int64) *item {
	it := &item{id: id, name: fmt.Sprintf("i-%06d", id), salt: salt, pad: make([]byte, 14)}
	for i := range it.pad {
		it.pad[i] = byte(id*3 + int64(i*9) + salt)
	}
	return it
}

func validItem(it *item, id, salt int64) bool {
	if it == nil || it.id != id || it.salt != salt || it.name != fmt.Sprintf("i-%06d", id) || len(it.pad) != 14 {
		return false
	}
	for i := range it.pad {
		if it.pad[i] != byte(id*3+int64(i*9)+salt) {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 4)

	for round := 0; round < rounds; round++ {
		salt := curSeed + int64(round)*101
		prefix := fmt.Sprintf("c%02d-", round)
		const n = 3000

		m := make(map[string]*item, n)
		keys := make([]string, 0, n)
		for i := 0; i < n; i++ {
			k := prefix + fmt.Sprintf("k%05d", i)
			m[k] = newItem(int64(i)+int64(round)*100000, salt)
			keys = append(keys, k)
		}
		ck(len(m) == n, "clear: initial len")

		clear(m)
		ck(len(m) == 0, "clear: len after clear")
		count := 0
		for range m {
			count++
		}
		ck(count == 0, "clear: range after clear")
		if _, ok := m[keys[0]]; ok {
			ck(false, "clear: stale key visible")
		}
		gcStress()
		ck(len(m) == 0, "clear: len after GC")

		// 同一张表重填，旧 value 指针不得复活。
		old0 := newItem(1, salt)
		m[keys[0]] = old0
		clear(m)
		gcStress()
		fresh0 := newItem(777777, salt+1)
		m[keys[0]] = fresh0
		got := m[keys[0]]
		ck(got == fresh0 && got != old0, "clear: stale value resurrected after refill")
		delete(m, keys[0])

		for i := 0; i < n; i++ {
			m[keys[i]] = newItem(int64(i)+200000, salt+2)
		}
		ck(len(m) == n, "clear: refill len")
		for i := 0; i < n; i += 5 {
			delete(m, keys[i])
		}
		clear(m)
		ck(len(m) == 0, "clear: len after delete+clear")
		gcStress()

		// nil / 空 map 上的 clear 必须是安全 no-op。
		var nilMap map[string]*item
		clear(nilMap)
		ck(len(nilMap) == 0, "clear: nil map len")
		nilMap = make(map[string]*item, 4)
		clear(nilMap)
		clear(nilMap)
		ck(len(nilMap) == 0, "clear: empty map len")
		nilMap[keys[0]] = newItem(42, salt)
		ck(len(nilMap) == 1, "clear: nil-derived map reuse")
		clear(nilMap)

		// 非指针 value 的 map 与 int key。
		im := make(map[int64]struct{}, 2048)
		for i := int64(0); i < 2048; i++ {
			im[i*int64(1+round%5)] = struct{}{}
		}
		before := len(im)
		clear(im)
		ck(before > 0 && len(im) == 0, "clear: int map")
		for i := int64(0); i < 2048; i++ {
			im[i] = struct{}{}
		}
		ck(len(im) == 2048, "clear: int map reuse")
		clear(im)

		// clear 外层 map 不影响仍被别名持有的内层 map。
		inner := map[string]*item{
			"a": newItem(9001, salt),
			"b": newItem(9002, salt+1),
			"c": newItem(9003, salt+2),
		}
		outer := map[string]map[string]*item{"g": inner, "h": {"x": newItem(9100, salt)}}
		alias := inner
		clear(outer)
		ck(len(outer) == 0 && len(alias) == 3, "clear: alias lost inner map")
		gcStress()
		ck(validItem(alias["a"], 9001, salt) && validItem(alias["b"], 9002, salt+1) && validItem(alias["c"], 9003, salt+2),
			"clear: alias payload corrupt after GC")

		// 与逐 key delete 循环的行为对照。
		m1 := make(map[string]*item, n)
		m2 := make(map[string]*item, n)
		for i := 0; i < n; i++ {
			m1[keys[i]] = newItem(int64(i)+300000, salt+3)
			m2[keys[i]] = newItem(int64(i)+300000, salt+3)
		}
		clear(m1)
		for k := range m2 {
			delete(m2, k)
		}
		ck(len(m1) == 0 && len(m2) == 0, "clear: delete loop parity len")
		for i := 0; i < n; i++ {
			m1[keys[i]] = newItem(int64(i)+400000, salt+4)
			m2[keys[i]] = newItem(int64(i)+400000, salt+4)
		}
		ck(len(m1) == n && len(m2) == n, "clear: delete loop parity refill")
		sortedKeys := append([]string(nil), keys...)
		sort.Strings(sortedKeys)
		for i := 0; i < n; i += 137 {
			if m1[sortedKeys[i]] == nil || m2[sortedKeys[i]] == nil {
				ckSeed(false, fmt.Sprintf("clear: parity lookup at round %d idx %d", round, i))
				break
			}
		}
		gcStress()
		ck(len(m1) == n && len(m2) == n, "clear: parity after GC")

		runtime.KeepAlive(m)
		runtime.KeepAlive(nilMap)
		runtime.KeepAlive(outer)
		runtime.KeepAlive(alias)
		runtime.KeepAlive(m1)
		runtime.KeepAlive(m2)
	}

	finish("l13_map_clear_reuse")
}
