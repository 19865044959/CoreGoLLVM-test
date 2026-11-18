// L09: C qsort 排序结构体数组，比较器经 //export 回调 Go。
// qsort 在 C 调用期内持有 Go 数组指针并反复回调；Go 比较器按
// score 降序/age 升序/name/id 升序排序，偶尔分配垃圾并显式 GC。
// C 不保留 Go 指针或回调函数指针到调用结束之后。
package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include "qs.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync/atomic"
	"unsafe"
)

var (
	failed   int32
	gcSink   [][]byte
	cmpCalls int64
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func personName(p *C.person_t) string {
	var b [12]byte
	n := 0
	for i := 0; i < len(p.name); i++ {
		c := byte(p.name[i])
		if c == 0 {
			break
		}
		b[n] = c
		n++
	}
	return string(b[:n])
}

type personKey struct {
	score, age, id int32
	name           string
}

//export goPersonCmp
func goPersonCmp(a, b unsafe.Pointer) C.int {
	pa := (*C.person_t)(a)
	pb := (*C.person_t)(b)
	cmpCalls++
	if cmpCalls%512 == 0 {
		junk := make([]byte, 2048)
		junk[0] = byte(cmpCalls)
		gcSink = append(gcSink[:0], junk)
		runtime.GC()
	}
	if pa.score != pb.score {
		if pa.score > pb.score {
			return -1
		}
		return 1
	}
	if pa.age != pb.age {
		if pa.age < pb.age {
			return -1
		}
		return 1
	}
	na, nb := personName(pa), personName(pb)
	if na != nb {
		if na < nb {
			return -1
		}
		return 1
	}
	if pa.id != pb.id {
		if pa.id < pb.id {
			return -1
		}
		return 1
	}
	return 0
}

// goScoreCmp 与排序方向一致（score 降序），bsearch 在该全序下导航。
//
//export goScoreCmp
func goScoreCmp(a, b unsafe.Pointer) C.int {
	pa := (*C.person_t)(a)
	pb := (*C.person_t)(b)
	cmpCalls++
	if pa.score == pb.score {
		return 0
	}
	if pa.score > pb.score {
		return -1
	}
	return 1
}

func makeRef(arr []C.person_t) []personKey {
	ref := make([]personKey, len(arr))
	for i := range arr {
		ref[i] = personKey{
			score: int32(arr[i].score),
			age:   int32(arr[i].age),
			id:    int32(arr[i].id),
			name:  personName(&arr[i]),
		}
	}
	sort.Slice(ref, func(i, j int) bool {
		if ref[i].score != ref[j].score {
			return ref[i].score > ref[j].score
		}
		if ref[i].age != ref[j].age {
			return ref[i].age < ref[j].age
		}
		if ref[i].name != ref[j].name {
			return ref[i].name < ref[j].name
		}
		return ref[i].id < ref[j].id
	})
	return ref
}

func checkSorted(arr []C.person_t, ref []personKey, tag string) {
	ck(len(arr) == len(ref), tag+": length")
	if len(arr) == 0 {
		return
	}
	for i := range ref {
		p := &arr[i]
		ck(int32(p.score) == ref[i].score, fmt.Sprintf("%s: score[%d]", tag, i))
		ck(int32(p.age) == ref[i].age, fmt.Sprintf("%s: age[%d]", tag, i))
		ck(int32(p.id) == ref[i].id, fmt.Sprintf("%s: id[%d]", tag, i))
		ck(personName(p) == ref[i].name, fmt.Sprintf("%s: name[%d]", tag, i))
	}
	ck(int32(C.qs_check_sorted(&arr[0], C.int32_t(len(arr)))) == 0, tag+": C sorted check")
}

func main() {
	const cname = "l09_cgo_qsort_struct"

	ck(uintptr(C.qs_sizeof_person()) == unsafe.Sizeof(C.person_t{}), "sizeof person_t")
	ck(uintptr(C.qs_off_name()) == unsafe.Offsetof(C.person_t{}.name), "offsetof person_t.name")
	ck(uintptr(C.qs_off_age()) == unsafe.Offsetof(C.person_t{}.age), "offsetof person_t.age")

	// Go slice：C 填充 → Go 参考排序 → C qsort（回调 Go 比较器）→ 逐字段对照。
	n := 200
	arr := make([]C.person_t, n)
	C.qs_fill(&arr[0], C.int32_t(n))
	ref := makeRef(arr)
	wantSum := int64(0)
	for _, k := range ref {
		wantSum += int64(k.score)
	}
	cmpCalls = 0
	C.qs_sort(&arr[0], C.int32_t(n))
	runtime.KeepAlive(arr)
	ck(cmpCalls > 0, "Go comparator invoked for Go slice")
	checkSorted(arr, ref, "go-slice")
	ck(int64(C.qs_sum_scores(&arr[0], C.int32_t(n))) == wantSum, "C sum after sort")

	// bsearch：按 score 全序查找若干个已存在键，并验证缺失键返回 -1。
	for _, idx := range []int{0, 1, 10, n / 2, n - 2, n - 1} {
		target := int32(arr[idx].score)
		pos := int32(C.qs_bsearch_score(&arr[0], C.int32_t(n), C.int32_t(target)))
		ck(pos == int32(idx), fmt.Sprintf("bsearch score[%d] position", idx))
		if pos >= 0 {
			ck(int32(arr[pos].id) == int32(arr[idx].id), fmt.Sprintf("bsearch score[%d] id", idx))
		}
	}
	ck(int32(C.qs_bsearch_score(&arr[0], C.int32_t(n), -12345)) == -1, "bsearch missing score")
	ck(int32(C.qs_bsearch_score(&arr[0], C.int32_t(n), 100003)) == -1, "bsearch out-of-set score")

	// n=0/1 边界：不触发比较器。
	before := cmpCalls
	one := make([]C.person_t, 1)
	C.qs_fill(&one[0], 1)
	C.qs_sort(&one[0], 1)
	ck(cmpCalls == before, "n=1 does not invoke comparator")
	ck(int32(C.qs_bsearch_score(nil, 0, 0)) == -1, "bsearch empty array")

	// C 拥有的数组：同一比较器路径，跨 GC 复核。
	cp := C.malloc(C.size_t(C.qs_sizeof_person() * C.size_t(128)))
	if cp == nil {
		ck(false, "malloc C-owned person array")
		finish(cname)
		return
	}
	defer C.free(cp)
	carr := unsafe.Slice((*C.person_t)(cp), 128)
	C.qs_fill(&carr[0], 128)
	cref := makeRef(carr)
	cmpCalls = 0
	C.qs_sort(&carr[0], 128)
	ck(cmpCalls > 0, "Go comparator invoked for C-owned array")
	checkSorted(carr, cref, "c-owned")
	ck(C.qs_check_sorted(&carr[0], 128) == 0, "c-owned C sorted check")
	gcStress()
	checkSorted(carr, cref, "c-owned-after-gc")

	// GC 后重新填充 Go slice 再排序，结果与参考一致。
	gcStress()
	arr2 := make([]C.person_t, 64)
	C.qs_fill(&arr2[0], 64)
	ref2 := makeRef(arr2)
	C.qs_sort(&arr2[0], 64)
	runtime.KeepAlive(arr2)
	checkSorted(arr2, ref2, "go-slice-after-gc")
	ck(int32(C.qs_check_sorted(&arr2[0], 64)) == 0, "post-GC C sorted check")
	ck(int64(C.qs_sum_scores(&arr2[0], 64)) == func() int64 {
		s := int64(0)
		for _, k := range ref2 {
			s += int64(k.score)
		}
		return s
	}(), "post-GC sum")

	finish(cname)
}
