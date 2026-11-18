// L09: C 侧排序实现（不回调 Go）：稳定插入排序按 score/age 排序结构体
// 数组，归并排序 int32 数组。输入分别是 Go slice 与 C calloc 数组，
// C 只在调用期内使用指针，不保留 Go 指针。
package main

/*
#include <stdint.h>
#include "c_sort.h"
*/
import "C"

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
)

type refPerson struct {
	id    int32
	age   int32
	score int32
	name  string
}

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

func refOf(p C.person_t) refPerson {
	var b []byte
	for _, c := range p.name {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return refPerson{id: int32(p.id), age: int32(p.age), score: int32(p.score), name: string(b)}
}

func fillRef(n int) []refPerson {
	out := make([]refPerson, n)
	for i := 0; i < n; i++ {
		out[i] = refPerson{
			id:    int32(i),
			age:   int32(20 + (i*13)%40),
			score: int32((i * 17) % 7),
			name:  fmt.Sprintf("p%03d", i),
		}
	}
	return out
}

func sortRef(ref []refPerson) {
	sort.SliceStable(ref, func(i, j int) bool {
		if ref[i].score != ref[j].score {
			return ref[i].score > ref[j].score
		}
		if ref[i].age != ref[j].age {
			return ref[i].age < ref[j].age
		}
		return false
	})
}

func collect(people []C.person_t) []refPerson {
	out := make([]refPerson, len(people))
	for i := range people {
		out[i] = refOf(people[i])
	}
	return out
}

func equalPeople(a, b []refPerson) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func peopleSum(p []refPerson) int64 {
	var s int64
	for _, v := range p {
		s += int64(v.id) + int64(v.age)*3 + int64(v.score)*5
	}
	return s
}

func main() {
	const n = 137
	people := make([]C.person_t, n)
	C.c_people_fill(&people[0], C.int32_t(n))
	ck(equalPeople(collect(people), fillRef(n)), "C people fill layout")

	ref := fillRef(n)
	sortRef(ref)

	C.c_sort_people(&people[0], C.int32_t(n))
	ck(int32(C.c_people_is_sorted(&people[0], C.int32_t(n))) == 1, "C reports sorted people")
	got := collect(people)
	ck(equalPeople(got, ref), "C stable sort matches Go SliceStable")
	ck(int64(C.c_people_checksum(&people[0], C.int32_t(n))) == peopleSum(ref), "C checksum after sort")

	// 显式验证稳定性：score/age 都相同的记录必须保持原 id 递增顺序。
	for i := 1; i < len(got); i++ {
		if got[i].score == got[i-1].score && got[i].age == got[i-1].age {
			ck(got[i].id > got[i-1].id, fmt.Sprintf("stable tie at i=%d", i))
		}
		if got[i].score > got[i-1].score {
			ck(false, fmt.Sprintf("score order at i=%d", i))
		}
	}

	gcStress()
	ck(equalPeople(collect(people), ref), "people sort result after GC")
	ck(int32(C.c_people_is_sorted(&people[0], C.int32_t(n))) == 1, "sorted after GC")
	runtime.KeepAlive(people)

	// C 归并排序 Go []int32，对照 Go sort.Slice；含 int32 两端极值。
	for _, m := range []int{0, 1, 2, 3, 64, 257} {
		if m == 0 {
			continue
		}
		ints := make([]int32, m)
		for i := range ints {
			ints[i] = int32((i*7919)%4001 - 2000)
		}
		if m >= 2 {
			ints[0] = math.MinInt32
			ints[m-1] = math.MaxInt32
		}
		want := append([]int32(nil), ints...)
		sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })

		C.c_sort_i32_asc((*C.int32_t)(unsafe.Pointer(&ints[0])), C.int32_t(m))
		ok := true
		for i := range ints {
			if ints[i] != want[i] {
				ok = false
				break
			}
		}
		ck(ok, fmt.Sprintf("C merge sort i32 n=%d", m))
		ck(ints[0] == want[0] && ints[m-1] == want[m-1], fmt.Sprintf("C merge sort endpoints n=%d", m))
		if m >= 64 {
			gcStress()
			ck(ints[0] == want[0] && ints[m-1] == want[m-1], fmt.Sprintf("C merge sort after GC n=%d", m))
		}
		runtime.KeepAlive(ints)
	}

	// C 拥有的结构体数组：allocate/fill/sort/read/free 全在 C 内存上。
	const cn = 64
	cp := C.c_people_new(cn)
	ck(cp != nil, "C-owned people array")
	if cp != nil {
		C.c_people_fill(cp, cn)
		ref2 := fillRef(cn)
		sortRef(ref2)
		C.c_sort_people(cp, cn)
		arr := unsafe.Slice(cp, cn)
		got2 := make([]refPerson, cn)
		for i := range arr {
			got2[i] = refOf(arr[i])
		}
		ck(equalPeople(got2, ref2), "C-owned people sorted")
		ck(int32(C.c_people_is_sorted(cp, cn)) == 1, "C-owned people is sorted")
		C.c_people_free(cp)
	}

	C.c_people_free(nil)
	finish("l09_cgo_c_sort")
}
