// L13: map[string]struct 值（含 string/slice/指针字段）的按值复制、覆盖、删除与 GC 后完整性。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type svInner struct {
	id   int64
	blob []byte
}

type svValue struct {
	id   int64
	name string
	in   svInner
	list []int64
	ptr  *svInner
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

func newSV(id, salt int64) svValue {
	v := svValue{
		id:   id,
		name: fmt.Sprintf("sv-%06d-%02d", id, salt%89),
		in:   svInner{id: id * 3, blob: make([]byte, 10)},
		list: make([]int64, 6),
	}
	for i := range v.in.blob {
		v.in.blob[i] = byte(id*11 + int64(i*5) + salt)
	}
	for i := range v.list {
		v.list[i] = id*7 + int64(i)
	}
	v.ptr = &svInner{id: id * 9, blob: []byte{byte(id), byte(id >> 8)}}
	return v
}

// svValid 允许第 0 个 blob/list 元素被共享底层数组的写入改写。
func svValid(v svValue, id, salt int64, blob0 byte, list0 int64) bool {
	if v.id != id || v.name != fmt.Sprintf("sv-%06d-%02d", id, salt%89) ||
		v.in.id != id*3 || len(v.in.blob) != 10 || len(v.list) != 6 ||
		v.ptr == nil || v.ptr.id != id*9 || len(v.ptr.blob) != 2 {
		return false
	}
	if v.in.blob[0] != blob0 || v.list[0] != list0 {
		return false
	}
	for i := 1; i < len(v.in.blob); i++ {
		if v.in.blob[i] != byte(id*11+int64(i*5)+salt) {
			return false
		}
	}
	for i := 1; i < len(v.list); i++ {
		if v.list[i] != id*7+int64(i) {
			return false
		}
	}
	return v.ptr.blob[0] == byte(id) && v.ptr.blob[1] == byte(id>>8)
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	n := 1400 + int(curSeed%100)
	m := make(map[string]svValue, n+1100)
	keys := make([]string, 0, n+1100)
	ids := make([]int64, 0, n+1100)
	present := make([]bool, 0, n+1100)

	for i := 0; i < n; i++ {
		key := fmt.Sprintf("sk-%05d-%02d", i, curSeed%97)
		id := int64(i) + curSeed%300
		m[key] = newSV(id, curSeed)
		keys = append(keys, key)
		ids = append(ids, id)
		present = append(present, true)
	}
	ck(len(m) == n, "struct value: initial len")

	// 值语义：标量/字符串/指针头按值复制；slice 字段仍共享底层数组。
	mutatedBlob0 := byte(ids[0]*11+curSeed) ^ 0x5a
	{
		k0 := keys[0]
		local := m[k0]
		local.id = -1
		local.name = "changed-local"
		local.ptr = nil
		stored := m[k0]
		ck(stored.id == ids[0] && stored.ptr != nil, "struct value: scalar fields copied by value")
		if stored.name != fmt.Sprintf("sv-%06d-%02d", ids[0], curSeed%89) {
			ckSeed(false, "struct value: name field aliased")
		}
		local.in.blob[0] ^= 0x5a
		if m[k0].in.blob[0] != mutatedBlob0 {
			ckSeed(false, "struct value: slice field did not share backing")
		}
		local.list[0] = -12345
		if m[k0].list[0] != -12345 {
			ckSeed(false, "struct value: list field did not share backing")
		}
	}

	perm := r.Perm(len(keys))
	validate := func(tag string) {
		for _, i := range perm {
			v, ok := m[keys[i]]
			if !present[i] {
				if ok {
					ckSeed(false, fmt.Sprintf("struct value: deleted key visible at %d (%s)", i, tag))
					return
				}
				continue
			}
			if !ok {
				ckSeed(false, fmt.Sprintf("struct value: key missing at %d (%s)", i, tag))
				return
			}
			blob0 := byte(ids[i]*11 + curSeed)
			list0 := ids[i] * 7
			if i == 0 {
				blob0 = mutatedBlob0
				list0 = -12345
			}
			if !svValid(v, ids[i], curSeed, blob0, list0) {
				ckSeed(false, fmt.Sprintf("struct value: content mismatch at %d (%s)", i, tag))
				return
			}
		}
	}
	validate("build")
	gcStress()
	validate("after build GC")

	// 覆盖：结构体值整体替换。
	for i := 5; i < len(keys); i += 5 {
		nv := newSV(ids[i]+400000, curSeed)
		m[keys[i]] = nv
		ids[i] = nv.id
	}
	ck(len(m) == n, "struct value: len after overwrite")
	validate("overwrite")
	gcStress()
	validate("after overwrite GC")

	// 删除后缺席；再重插不同 id 的值。
	for i := 7; i < len(keys); i += 7 {
		delete(m, keys[i])
		present[i] = false
	}
	ck(len(m) < n, "struct value: len after delete")
	validate("delete")
	gcStress()
	validate("after delete GC")
	for i := 7; i < len(keys); i += 7 {
		nv := newSV(ids[i]+900000, curSeed)
		m[keys[i]] = nv
		ids[i] = nv.id
		present[i] = true
	}
	ck(len(m) == n, "struct value: len after reinsert")
	perm = r.Perm(len(keys))
	validate("reinsert")

	// 扩容到 2500+ 项。
	total := len(keys)
	for i := n; i < 2500; i++ {
		key := fmt.Sprintf("sk-%05d-%02d", i, curSeed%97)
		id := int64(i) + 1000000
		m[key] = newSV(id, curSeed)
		keys = append(keys, key)
		ids = append(ids, id)
		present = append(present, true)
		total++
	}
	ck(len(m) == total, "struct value: len after growth")
	perm = r.Perm(total)
	validate("growth")
	gcStress()
	validate("after growth GC")

	var sum int64
	var count int
	for _, v := range m {
		count++
		sum += v.id % 1000
	}
	var wantSum int64
	var wantCount int
	for i := range ids {
		if present[i] {
			wantCount++
			wantSum += ids[i] % 1000
		}
	}
	ck(count == wantCount, "struct value: iteration count")
	ck(sum == wantSum, "struct value: iteration aggregate")

	gcStress()
	validate("final")
	runtime.KeepAlive(m)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(ids)
	finish("l13_map_struct_value_gc")
}
