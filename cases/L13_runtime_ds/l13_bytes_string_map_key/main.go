// L13: []byte→string 作为 map key：转换拷贝的所有权、二进制 key、重复插入/删除与扩容。
package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

type entry struct {
	id   int
	key  string
	blob []byte
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

func fillKey(dst []byte, i int, r *rand.Rand) {
	binary.LittleEndian.PutUint32(dst[:4], uint32(i))
	for j := 4; j < len(dst); j++ {
		switch (i + j) % 5 {
		case 0:
			dst[j] = 0
		case 1:
			dst[j] = 0xFF
		case 2:
			dst[j] = 0x80 // 非法 UTF-8 首字节
		default:
			dst[j] = byte(r.Intn(256))
		}
	}
}

func validEntry(e *entry, id int, key string) bool {
	if e == nil || e.id != id || e.key != key || string(e.blob) != key {
		return false
	}
	return true
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	const n = 2000
	m := make(map[string]*entry, 64) // 小容量起步，强制多次扩容
	want := make(map[string]int, n)
	keys := make([]string, 0, n)
	buf := make([]byte, 128)

	for i := 0; i < n; i++ {
		ln := 8 + (i*37)%57
		seg := buf[:ln]
		fillKey(seg, i, r)
		k := string(seg) // 必须发生拷贝
		keys = append(keys, k)
		blob := append([]byte(nil), seg...)
		m[k] = &entry{id: i, key: k, blob: blob}
		want[k] = i

		// 覆盖源缓冲，之后原 key 必须仍然可查且内容不变。
		for j := range seg {
			seg[j] = 0xEE
		}
		if e, ok := m[k]; !ok || !validEntry(e, i, k) {
			ckSeed(false, fmt.Sprintf("inserted byte-key lost after source mutation at %d", i))
			break
		}
		if i%257 == 0 {
			gcStress()
		}
	}
	ckSeed(len(m) == n, "map size after byte-key inserts")
	ckSeed(len(want) == n, "reference size after byte-key inserts")

	// 从全新缓冲重建 key 查询（不同底层数组，同内容）。
	fresh := make([]byte, 128)
	for i := 0; i < n; i++ {
		seg := fresh[:len(keys[i])]
		copy(seg, keys[i])
		k2 := string(seg)
		e, ok := m[k2]
		if !ok || !validEntry(e, i, keys[i]) {
			ckSeed(false, fmt.Sprintf("lookup with reconstructed key failed at %d", i))
			break
		}
		for j := range seg {
			seg[j] = 0x11
		}
		if _, ok := m[k2]; !ok {
			ckSeed(false, fmt.Sprintf("reconstructed key invalidated by its source buffer at %d", i))
			break
		}
	}

	// 重复内容从不同缓冲插入：不新增条目，值被替换。
	dup := make([]byte, 128)
	for i := 0; i < n; i += 100 {
		seg := dup[:len(keys[i])]
		copy(seg, keys[i])
		k := string(seg)
		m[k] = &entry{id: i + 100000, key: k, blob: append([]byte(nil), seg...)}
		want[k] = i + 100000
	}
	ckSeed(len(m) == n, "duplicate byte-key insert changed map size")
	for i := 0; i < n; i += 100 {
		e, ok := m[keys[i]]
		ckSeed(ok && validEntry(e, i+100000, keys[i]), fmt.Sprintf("duplicate key value at %d", i))
	}

	// 用另建的 []byte 删除每 7 个 key，再原样重建部分。
	deleted := 0
	for i := 0; i < n; i += 7 {
		tmp := []byte(keys[i])
		k := string(tmp)
		delete(m, k)
		delete(want, k)
		deleted++
	}
	gcStress()
	ckSeed(len(m) == n-deleted, "map size after byte-key deletes")

	rebuilt := 0
	for i := 0; i < n; i += 14 {
		tmp := append([]byte(nil), []byte(keys[i])...)
		k := string(tmp)
		m[k] = &entry{id: i + 200000, key: k, blob: append([]byte(nil), tmp...)}
		want[k] = i + 200000
		rebuilt++
	}
	gcStress()
	ckSeed(len(m) == n-deleted+rebuilt, "map size after byte-key reinsert")

	// 单字节 key 0x00..0xFF 与空 key。
	for b := 0; b < 256; b++ {
		one := []byte{byte(b)}
		k := string(one)
		m[k] = &entry{id: 500000 + b, key: k, blob: append([]byte(nil), one...)}
		want[k] = 500000 + b
	}
	m[""] = &entry{id: 600000, key: "", blob: []byte{}}
	want[""] = 600000
	gcStress()
	for b := 0; b < 256; b++ {
		one := []byte{byte(b)}
		e, ok := m[string(one)]
		ckSeed(ok && validEntry(e, 500000+b, string(one)), fmt.Sprintf("single-byte key 0x%02x", b))
	}
	e, ok := m[""]
	ckSeed(ok && validEntry(e, 600000, ""), "empty string key")

	// 4KB 大 key：写入后改写源缓冲，GC 后仍可查。
	big := make([]byte, 4096)
	fillKey(big, 777777, r)
	bigK := string(big)
	m[bigK] = &entry{id: 777777, key: bigK, blob: append([]byte(nil), big...)}
	want[bigK] = 777777
	for i := range big {
		big[i] ^= 0xA5
	}
	gcStress()
	e, ok = m[bigK]
	ckSeed(ok && validEntry(e, 777777, bigK), "4KB byte-key after source mutation and GC")

	// 全量确定性校验：排序 key、长度、值与 blob 内容。
	keysA := make([]string, 0, len(m))
	for k := range m {
		keysA = append(keysA, k)
	}
	keysB := make([]string, 0, len(want))
	for k := range want {
		keysB = append(keysB, k)
	}
	sort.Strings(keysA)
	sort.Strings(keysB)
	if len(keysA) != len(keysB) {
		ckSeed(false, "final key count differs from reference")
	} else {
		for i := range keysA {
			if keysA[i] != keysB[i] {
				ckSeed(false, "final sorted keys differ from reference")
				break
			}
		}
	}
	for _, k := range keysA {
		e := m[k]
		id := want[k]
		if !validEntry(e, id, k) {
			ckSeed(false, fmt.Sprintf("final entry mismatch for key len=%d", len(k)))
			break
		}
	}
	gcStress()
	for _, k := range keysA {
		if !validEntry(m[k], want[k], k) {
			ckSeed(false, "final entry mismatch after GC")
			break
		}
	}

	runtime.KeepAlive(m)
	runtime.KeepAlive(want)
	runtime.KeepAlive(keys)
	finish("l13_bytes_string_map_key")
}
