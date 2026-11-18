// L18: 老 map（老 key）批量写入年轻 value，并持续插入新 key 触发扩容；
// setter noinline，返回后新 key/value 只被老 map 引用，再 GC 校验。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	baseKeys  = 128
	insertPer = 128
	valueLen  = 48
)

type value struct {
	id   int
	ver  int
	tag  string
	data []byte
}

type store struct {
	m map[string]*value
}

var (
	failed int32
	seed   int64 = 1
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
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
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func keyOf(i int) string { return "k" + strconv.Itoa(i) }

func patternByte(id, ver, i int) byte { return byte((id*31 + ver*17 + i*7) % 251) }

func newValue(id, ver int) *value {
	v := &value{id: id, ver: ver, tag: "mv", data: make([]byte, valueLen)}
	for i := range v.data {
		v.data[i] = patternByte(id, ver, i)
	}
	return v
}

func checkValue(v *value, id, ver int) bool {
	if v == nil || v.id != id || v.ver != ver || v.tag != "mv" || len(v.data) != valueLen {
		return false
	}
	for i := range v.data {
		if v.data[i] != patternByte(id, ver, i) {
			return false
		}
	}
	return true
}

// rewriteExisting 覆写老 key 的值为年轻 value。
//
//go:noinline
func rewriteExisting(m map[string]*value, k string, id, ver int) {
	m[k] = newValue(id, ver)
}

// insertNew 用年轻 key + 年轻 value 插入，扩容也在 setter 内发生。
//
//go:noinline
func insertNew(m map[string]*value, id, ver int) {
	k := keyOf(id)
	m[k] = newValue(id, ver)
}

func verify(st *store, keys []string, want []int, n int) {
	for id := 0; id < n; id++ {
		var v *value
		if id < baseKeys {
			v = st.m[keys[id]] // 必须仍能由老 key 命中
		} else {
			v = st.m[keyOf(id)]
		}
		if !checkValue(v, id, want[id]) {
			ck(false, fmt.Sprintf("map entry id=%d ver=%d corrupted", id, want[id]))
		}
	}
}

func parseSeed() int64 {
	v := os.Getenv("UAF_SEED")
	if v == "" {
		return 1
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 32 {
		n = 32
	}
	return n
}

func main() {
	seed = parseSeed()
	rng := rand.New(rand.NewSource(seed))
	rounds := tortureRounds(6)

	keys := make([]string, baseKeys)
	st := &store{m: make(map[string]*value, baseKeys)}
	for i := 0; i < baseKeys; i++ {
		keys[i] = keyOf(i)
		st.m[keys[i]] = newValue(i, 0)
	}
	want := make([]int, baseKeys+rounds*insertPer)
	gcStress()
	gcStress() // map、老 key、初始 value 晋升为老对象
	verify(st, keys, want, baseKeys)

	nextID := baseKeys
	for r := 1; r <= rounds; r++ {
		// 随机顺序覆写全部老 key：新 value 仅由老 map 引用。
		for j, idx := range rng.Perm(baseKeys) {
			rewriteExisting(st.m, keys[idx], idx, r)
			want[idx] = r
			if j%32 == 31 {
				gcStress()
			}
		}
		// 插入新 key/value 触发 map 扩容（128 -> 128+128*rounds）。
		for j := 0; j < insertPer; j++ {
			id := nextID
			nextID++
			insertNew(st.m, id, r)
			want[id] = r
		}
		nextID = baseKeys + r*insertPer

		gcStress()
		verify(st, keys, want, nextID)
		ck(len(st.m) == baseKeys+r*insertPer, "map size drift after growth")
	}

	gcStress()
	verify(st, keys, want, len(want))
	ck(len(st.m) == len(want), "final map size mismatch")
	runtime.KeepAlive(st)
	runtime.KeepAlive(keys)
	finish("l18_map_old_key_new_value_grow")
}
