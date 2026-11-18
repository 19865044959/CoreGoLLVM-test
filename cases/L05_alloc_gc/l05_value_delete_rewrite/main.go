// L05: 模拟 badger value log：批量写入 32KB 大值，部分删除后 GC，再全量重写并 GC；
// 旧值只要仍被引用就必须保持完整，删除的 key 必须消失。
package main

import (
	"fmt"
	"hash/crc32"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	valueCount = 96
	valueSize  = 32 << 10
)

var (
	failed int32
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
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

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func keyName(k int) string {
	return fmt.Sprintf("key-%03d", k)
}

type value struct {
	data []byte
	crc  uint32
	ver  int
}

func newValue(k, ver int, seed uint64) *value {
	data := make([]byte, valueSize)
	base := uint64(k)*37 + uint64(ver)*101 + seed*13 + 5
	for i := range data {
		data[i] = byte((base + uint64(i)*7) % 251)
	}
	return &value{data: data, crc: crc32.ChecksumIEEE(data), ver: ver}
}

func checkValue(v *value, k, ver int, seed uint64) {
	if v == nil {
		ckf(false, "key=%s missing (want ver=%d)", keyName(k), ver)
		return
	}
	if v.ver != ver {
		ckf(false, "key=%s ver=%d want=%d", keyName(k), v.ver, ver)
	}
	if len(v.data) != valueSize {
		ckf(false, "key=%s len=%d want=%d", keyName(k), len(v.data), valueSize)
		return
	}
	if crc32.ChecksumIEEE(v.data) != v.crc {
		ckf(false, "key=%s stored checksum mismatch", keyName(k))
	}
	// 逐段抽样校验内容与当前版本模式一致，避免只剩 CRC 自洽。
	base := uint64(k)*37 + uint64(ver)*101 + seed*13 + 5
	for i := 0; i < valueSize; i += 4096 {
		if v.data[i] != byte((base+uint64(i)*7)%251) {
			ckf(false, "key=%s content mismatch at=%d ver=%d", keyName(k), i, ver)
			break
		}
	}
}

func main() {
	seed := loadSeed()

	store := make(map[string]*value, valueCount)
	for k := 0; k < valueCount; k++ {
		store[keyName(k)] = newValue(k, 1, seed)
	}
	gcStress()

	for k := 0; k < valueCount; k++ {
		checkValue(store[keyName(k)], k, 1, seed)
	}

	// 保留一个旧值指针，重写后它仍必须可读（GC 不能回收被引用的旧对象）。
	held := store[keyName(1)]
	heldCRC := crc32.ChecksumIEEE(held.data)

	// 部分删除：约每 3 个删 1 个，删除后再强制 GC。
	delEvery := 3
	if seed%2 == 0 {
		delEvery = 4
	}
	deleted := make(map[string]bool)
	deletedNames := make([]string, 0, valueCount/delEvery+1)
	for k := 0; k < valueCount; k += delEvery {
		name := keyName(k)
		delete(store, name)
		deleted[name] = true
		deletedNames = append(deletedNames, name)
	}
	gcStress()

	for k := 0; k < valueCount; k++ {
		name := keyName(k)
		if deleted[name] {
			if _, ok := store[name]; ok {
				ckf(false, "key=%s should be deleted", name)
			}
			continue
		}
		checkValue(store[name], k, 1, seed)
	}

	// 全量重写：所有 key 生成 ver=2 的新值，逐批替换并穿插 GC。
	for k := 0; k < valueCount; k++ {
		store[keyName(k)] = newValue(k, 2, seed)
		if k%16 == 15 {
			gcStress()
		}
	}
	gcStress()

	for k := 0; k < valueCount; k++ {
		checkValue(store[keyName(k)], k, 2, seed)
	}

	// 被 held 引用的旧对象在多次 GC 后必须仍然有效且内容不变。
	ck(held != nil && crc32.ChecksumIEEE(held.data) == heldCRC, "held old value corrupted after rewrite+GC")
	checkValue(held, 1, 1, seed)

	// 删除的 key 在全量重写后也必须已恢复（重写覆盖所有 key）。
	for _, name := range deletedNames {
		if store[name] == nil {
			ckf(false, "key=%s not restored by rewrite", name)
		}
	}

	runtime.KeepAlive(held)
	runtime.KeepAlive(store)
	finish("l05_value_delete_rewrite", seed)
}
