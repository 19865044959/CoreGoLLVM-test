// L18: 32KB 大值批量写入 -> 部分删除 -> 全量重写；老对象 slice 指向新对象，GC 后校验未删值。
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
	valueSize = 32 << 10
	keyCount  = 64
)

var (
	failed int32
	seed   int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

type blob struct {
	key  int
	ver  int
	data []byte
}

func patternByte(key, ver, pos int) byte {
	return byte((key*31 + ver*17 + pos*7) % 251)
}

func makeBlob(key, ver int) *blob {
	b := &blob{key: key, ver: ver, data: make([]byte, valueSize)}
	for i := range b.data {
		b.data[i] = patternByte(key, ver, i)
	}
	return b
}

func checkBlob(b *blob, key, ver int) bool {
	if b == nil || b.key != key || b.ver != ver || len(b.data) != valueSize {
		return false
	}
	for i := range b.data {
		if b.data[i] != patternByte(key, ver, i) {
			return false
		}
	}
	return true
}

type store struct {
	entries []*blob
}

// storeSet 在 noinline 函数内分配并写入；返回后新对象只被老 store.entries 引用。
//
//go:noinline
func storeSet(st *store, key, ver int) {
	st.entries[key] = makeBlob(key, ver)
}

//go:noinline
func storeDelete(st *store, key int) {
	st.entries[key] = nil
}

func verifyStore(st *store, deleted []bool, wantVer int) {
	for k := 0; k < keyCount; k++ {
		if deleted[k] {
			if st.entries[k] != nil {
				ck(false, fmt.Sprintf("deleted key %d resurrected", k))
			}
			continue
		}
		if !checkBlob(st.entries[k], k, wantVer) {
			ck(false, fmt.Sprintf("live key %d corrupted at ver %d", k, wantVer))
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

func main() {
	seed = parseSeed()
	rng := rand.New(rand.NewSource(seed))

	st := &store{entries: make([]*blob, keyCount)}
	for k := 0; k < keyCount; k++ {
		st.entries[k] = makeBlob(k, 1)
	}

	// 晋升为老对象：store/entries/初始值全部经历多轮 GC。
	gcStress()
	gcStress()
	verifyStore(st, make([]bool, keyCount), 1)

	// 部分删除（删除集合由 UAF_SEED 决定）。
	deleted := make([]bool, keyCount)
	order := rng.Perm(keyCount)
	delCount := 35 + rng.Intn(16) // 35..50
	for i := 0; i < delCount; i++ {
		k := order[i]
		deleted[k] = true
		storeDelete(st, k)
		if i%8 == 0 {
			gcStress()
		}
	}
	gcStress()
	verifyStore(st, deleted, 1)

	// 全量重写未删除的 key：每写入一个新对象后立即 GC（setter 已返回，旧栈帧不再持有新对象）。
	for ver := 2; ver <= 3; ver++ {
		for k := 0; k < keyCount; k++ {
			if deleted[k] {
				continue
			}
			storeSet(st, k, ver)
			gcStress()
			if !checkBlob(st.entries[k], k, ver) {
				ck(false, fmt.Sprintf("new value %d (ver %d) collected after write barrier GC", k, ver))
			}
		}
		gcStress()
		verifyStore(st, deleted, ver)
	}

	// 重写后删除集仍必须为空。
	for k := 0; k < keyCount; k++ {
		if deleted[k] {
			ck(st.entries[k] == nil, fmt.Sprintf("deleted key %d not nil after rewrite", k))
		}
	}
	runtime.KeepAlive(st)

	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED l18_value_delete_rewrite_gc (%d failed checks, seed=%d)\n", n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS l18_value_delete_rewrite_gc")
}
