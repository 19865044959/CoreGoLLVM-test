// L05: 大 struct 作为 map value（按值存储，含 [1024]byte、string、[]byte、指针），
// 覆盖插入/覆盖/删除/整表丢弃；GC + 垃圾分配后校验 map 内值与独立副本，
// 证明 bucket 扫描与按值复制不丢指针字段。默认 2 轮，TORTURE_ROUNDS 可放大。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	mvEntries = 700
	mvSize    = 1024
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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		return hi
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

func gcStress() {
	for i := 0; i < 20; i++ {
		b := make([]byte, 32<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

type mvMeta struct {
	tag string
	num int64
}

type mvVal struct {
	id    int64
	ver   int32
	name  string
	fixed [mvSize]byte
	tail  []byte
	ptr   *mvMeta
}

func mvName(key int64, ver int32) string {
	return fmt.Sprintf("v-%d-%d", key, ver)
}

func mvTailLen(key int64, ver int32, seed uint64) int {
	return 32 + int((uint64(key)*7+uint64(ver)*13+seed)%129)
}

func mvTailByte(key int64, ver int32, off int, seed uint64) byte {
	return byte((seed*11 + uint64(key)*17 + uint64(ver)*29 + uint64(off)*5) % 251)
}

func mvFill(v *mvVal, key int64, ver int32, seed uint64) {
	v.id = key
	v.ver = ver
	v.name = mvName(key, ver)
	for i := 0; i < mvSize; i++ {
		v.fixed[i] = byte((seed*7 + uint64(key)*13 + uint64(ver)*19 + uint64(i)*3) % 251)
	}
	n := mvTailLen(key, ver, seed)
	v.tail = make([]byte, n)
	for i := range v.tail {
		v.tail[i] = mvTailByte(key, ver, i, seed)
	}
	v.ptr = &mvMeta{tag: fmt.Sprintf("m-%d-%d", key, ver), num: key*3 + int64(ver)}
}

func mvCheck(tag string, key int64, ver int32, v *mvVal, seed uint64) {
	ckf(v.id == key, "%s key=%d id=%d", tag, key, v.id)
	ckf(v.ver == ver, "%s key=%d ver=%d want=%d", tag, key, v.ver, ver)
	ckf(v.name == mvName(key, ver), "%s key=%d name=%q", tag, key, v.name)
	ckf(v.ptr != nil, "%s key=%d nil ptr", tag, key)
	if v.ptr != nil {
		ckf(v.ptr.tag == fmt.Sprintf("m-%d-%d", key, ver), "%s key=%d meta tag=%q", tag, key, v.ptr.tag)
		ckf(v.ptr.num == key*3+int64(ver), "%s key=%d meta num=%d", tag, key, v.ptr.num)
	}
	ckf(len(v.tail) == mvTailLen(key, ver, seed), "%s key=%d tail len=%d want=%d", tag, key, len(v.tail), mvTailLen(key, ver, seed))
	for i := 0; i < len(v.tail); i += 17 {
		if v.tail[i] != mvTailByte(key, ver, i, seed) {
			ckf(false, "%s key=%d tail byte=%d at=%d", tag, key, v.tail[i], i)
			break
		}
	}
	for i := 0; i < mvSize; i += 61 {
		if v.fixed[i] != byte((seed*7+uint64(key)*13+uint64(ver)*19+uint64(i)*3)%251) {
			ckf(false, "%s key=%d fixed byte mismatch at=%d", tag, key, i)
			break
		}
	}
	if v.fixed[0] != byte((seed*7+uint64(key)*13+uint64(ver)*19)%251) || v.fixed[mvSize-1] != byte((seed*7+uint64(key)*13+uint64(ver)*19+uint64(mvSize-1)*3)%251) {
		ckf(false, "%s key=%d fixed boundary mismatch", tag, key)
	}
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 8)
	finalPins := make([]mvVal, 0, rounds)

	for round := 0; round < rounds; round++ {
		base := int64(round) * 10000
		m := make(map[int64]mvVal, mvEntries)
		for i := 0; i < mvEntries; i++ {
			key := base + int64(i)
			var v mvVal
			mvFill(&v, key, 1, seed)
			m[key] = v
		}

		// 独立副本与 map 中的按值对象共享底层指针，但结构体字段被复制。
		copies := make(map[int64]mvVal, mvEntries)
		for i := 0; i < mvEntries; i++ {
			key := base + int64(i)
			copies[key] = m[key]
		}

		gcStress()
		for i := 0; i < mvEntries; i++ {
			key := base + int64(i)
			v, ok := m[key]
			ckf(ok, "round=%d key=%d missing after build+GC", round, key)
			if ok {
				mvCheck("map", key, 1, &v, seed)
			}
			cv := copies[key]
			mvCheck("copy", key, 1, &cv, seed)
			ckf(v.ptr == cv.ptr, "round=%d key=%d copy lost pointer identity", round, key)
		}

		// 覆盖 1/3，保留被覆盖前的旧值指针，随后 GC 校验旧值不受新值影响。
		var heldOld mvVal
		heldKey := base
		for i := 0; i < mvEntries; i++ {
			if i%3 != 0 {
				continue
			}
			key := base + int64(i)
			if i == 0 {
				heldOld = m[key]
			}
			var nv mvVal
			mvFill(&nv, key, 2, seed)
			m[key] = nv
		}

		// 删除 1/5。
		for i := 0; i < mvEntries; i++ {
			if i%5 == 0 {
				delete(m, base+int64(i))
			}
		}

		gcStress()
		for i := 0; i < mvEntries; i++ {
			key := base + int64(i)
			v, ok := m[key]
			if i%5 == 0 {
				ckf(!ok, "round=%d key=%d still present after delete", round, key)
				ckf(copies[key].ver == 1, "round=%d key=%d copy changed after delete", round, key)
				continue
			}
			ckf(ok, "round=%d key=%d missing after churn+GC", round, key)
			if !ok {
				continue
			}
			wantVer := int32(1)
			if i%3 == 0 {
				wantVer = 2
			}
			mvCheck("map-churn", key, wantVer, &v, seed)
			if i%3 == 0 {
				// 覆盖前保存的副本必须仍是 ver=1。
				cv := copies[key]
				mvCheck("copy-after-update", key, 1, &cv, seed)
				ckf(v.ptr != cv.ptr, "round=%d key=%d update reused old meta pointer", round, key)
			}
		}
		mvCheck("held-old", heldKey, 1, &heldOld, seed)

		// 整表丢弃 + 垃圾 + GC：独立副本仍必须有效。
		pin := copies[base+int64(mvEntries-2)]
		m = nil
		copies = nil
		gcStress()
		mvCheck("pin-after-drop", base+int64(mvEntries-2), 1, &pin, seed)
		finalPins = append(finalPins, pin)
	}

	gcStress()
	for i := range finalPins {
		mvCheck("final-pin", finalPins[i].id, finalPins[i].ver, &finalPins[i], seed)
	}
	ckf(len(finalPins) == rounds, "final pins=%d want=%d", len(finalPins), rounds)

	finish("l05_map_large_value_copy", seed)
}
