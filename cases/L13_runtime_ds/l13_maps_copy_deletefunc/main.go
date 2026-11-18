// L13: maps.Copy/DeleteFunc/Clone/Equal 与 maps.Keys/Values 迭代器：浅拷贝别名、集合聚合与 GC。
package main

import (
	"fmt"
	"maps"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
)

type me struct {
	id   int64
	name string
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

func newME(id, salt int64) *me {
	e := &me{id: id, name: fmt.Sprintf("me-%06d-%02d", id, salt%67), pad: make([]byte, 10)}
	for i := range e.pad {
		e.pad[i] = byte(id*29 + int64(i*7) + salt)
	}
	return e
}

func meValid(e *me, salt int64) bool {
	if e == nil || e.name != fmt.Sprintf("me-%06d-%02d", e.id, salt%67) || len(e.pad) != 10 {
		return false
	}
	for i := range e.pad {
		if e.pad[i] != byte(e.id*29+int64(i*7)+salt) {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()

	n := 1000 + int(curSeed%100)
	src := make(map[string]*me, n)
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		k := fmt.Sprintf("mk-%05d-%02d", i, curSeed%71)
		src[k] = newME(int64(i)+curSeed%400, curSeed)
		keys = append(keys, k)
	}
	ck(len(src) == n, "maps: initial len")

	// Copy：dst 中与 src 重叠的键被覆盖，仅 dst 有的键保留。
	dst := make(map[string]*me, n+1)
	for i := 0; i < n; i += 3 {
		dst[keys[i]] = newME(-1, curSeed)
	}
	extraKey := "zzz-extra"
	dst[extraKey] = newME(-2, curSeed)
	maps.Copy(dst, src)
	ck(len(dst) == n+1, "maps.Copy: len")
	for i, k := range keys {
		if dst[k] != src[k] {
			ckSeed(false, fmt.Sprintf("maps.Copy: pointer mismatch at %d", i))
			break
		}
	}
	ck(dst[extraKey] != nil && dst[extraKey].id == -2, "maps.Copy: extra destination key lost")

	// 浅拷贝：指针目标共享。
	src[keys[0]].name = "shared-mutation"
	ck(dst[keys[0]].name == "shared-mutation", "maps.Copy: value target not shared")
	src[keys[0]].name = fmt.Sprintf("me-%06d-%02d", src[keys[0]].id, curSeed%67)

	// DeleteFunc：删除 id%7==0 与额外键。
	maps.DeleteFunc(dst, func(k string, v *me) bool {
		return v.id%7 == 0 || k == extraKey
	})
	var wantKeys []string
	var wantValSum int64
	for _, k := range keys {
		if src[k].id%7 != 0 {
			wantKeys = append(wantKeys, k)
			wantValSum += src[k].id % 1000
		}
	}
	slices.Sort(wantKeys)
	gotKeys := slices.Sorted(maps.Keys(dst))
	ck(slices.Equal(gotKeys, wantKeys), "maps.Keys: key set after DeleteFunc")
	ck(len(dst) == len(wantKeys), "maps.DeleteFunc: len")

	vals := slices.Collect(maps.Values(dst))
	ck(len(vals) == len(wantKeys), "maps.Values: count")
	var valSum int64
	for _, v := range vals {
		valSum += v.id % 1000
	}
	ck(valSum == wantValSum, "maps.Values: aggregate")
	if _, ok := dst[extraKey]; ok {
		ckSeed(false, "maps.DeleteFunc: extra key survived")
	}

	// Clone 与 Equal：浅拷贝 + 指针相等；修改克隆不影响源。
	clone := maps.Clone(src)
	delete(clone, keys[1])
	ck(len(clone) == n-1, "maps.Clone: len after delete")
	ck(len(src) == n, "maps.Clone: source length changed")
	ck(clone[keys[2]] == src[keys[2]], "maps.Clone: pointer identity")
	ck(maps.Equal(src, src), "maps.Equal: identical map")
	changed := maps.Clone(src)
	changed[keys[3]] = newME(-3, curSeed)
	ck(!maps.Equal(changed, src), "maps.Equal: different pointer not detected")
	changed[keys[3]] = src[keys[3]]
	ck(maps.Equal(changed, src), "maps.Equal: restored map not equal")

	// nil map 上的 Copy/DeleteFunc/Clone/Equal 必须安全。
	var nilMap map[string]*me
	maps.Copy(dst, nilMap) // nil 源：无操作
	maps.DeleteFunc(nilMap, func(string, *me) bool { return true })
	ck(len(nilMap) == 0, "maps: nil destination modified")
	if maps.Clone(nilMap) != nil {
		ckSeed(false, "maps.Clone: nil map did not stay nil")
	}
	ck(maps.Equal(nilMap, nilMap), "maps.Equal: nil maps not equal")

	gcStress()
	for i, k := range keys {
		if !meValid(src[k], curSeed) {
			ckSeed(false, fmt.Sprintf("maps: source payload corrupted at %d after GC", i))
			break
		}
	}
	for _, v := range dst {
		if !meValid(v, curSeed) {
			ckSeed(false, "maps: destination payload corrupted after GC")
			break
		}
	}
	gcStress()
	gotKeys2 := slices.Sorted(maps.Keys(dst))
	ck(slices.Equal(gotKeys2, wantKeys), "maps.Keys: key set after GC")

	runtime.KeepAlive(src)
	runtime.KeepAlive(dst)
	runtime.KeepAlive(clone)
	runtime.KeepAlive(changed)
	runtime.KeepAlive(keys)
	finish("l13_maps_copy_deletefunc")
}
