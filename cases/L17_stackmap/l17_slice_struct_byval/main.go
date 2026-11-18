// L17: 含指针字段的结构体/结构体切片/数组按值传递，栈上聚合副本跨 GC 仍有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type item struct {
	v    int64
	name string
}

type record struct {
	name string
	head *item
	list []*item
	arr  [2]*item
	strs []string
	data []byte
}

var failed int32

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func mkRecord(i int64) record {
	r := record{
		name: "record",
		head: &item{v: i*16 + 1, name: "head"},
		list: []*item{
			{v: i*16 + 2, name: "l0"},
			{v: i*16 + 3, name: "l1"},
			{v: i*16 + 4, name: "l2"},
		},
		arr:  [2]*item{{v: i*16 + 5, name: "a0"}, {v: i*16 + 6, name: "a1"}},
		strs: []string{"s0", "s1"},
		data: make([]byte, 64),
	}
	for j := range r.data {
		r.data[j] = byte((int(i) + j) % 253)
	}
	return r
}

func ptrSum(r record) int64 {
	return r.head.v + r.list[0].v + r.list[1].v + r.list[2].v + r.arr[0].v + r.arr[1].v
}

func checkRecord(r record, i int64) bool {
	if r.name != "record" || r.head == nil || r.head.v != i*16+1 || r.head.name != "head" {
		return false
	}
	if len(r.list) != 3 || len(r.arr) != 2 {
		return false
	}
	for k, it := range r.list {
		if it == nil || it.v != i*16+2+int64(k) {
			return false
		}
	}
	if r.arr[0] == nil || r.arr[0].v != i*16+5 || r.arr[1] == nil || r.arr[1].v != i*16+6 {
		return false
	}
	if len(r.strs) != 2 || r.strs[0] != "s0" || r.strs[1] != "s1" {
		return false
	}
	if len(r.data) != 64 {
		return false
	}
	for j := range r.data {
		if r.data[j] != byte((int(i)+j)%253) {
			return false
		}
	}
	return true
}

// checkByVal 按值接收聚合，在 GC 后校验其内部的指针/字符串/切片。
//
//go:noinline
func checkByVal(r record, i int64) bool {
	gcStress()
	return checkRecord(r, i)
}

//go:noinline
func sumByVal(r record) int64 {
	gcStress()
	return ptrSum(r)
}

//go:noinline
func sumSliceByVal(rs []record) int64 {
	gcStress()
	var s int64
	for _, r := range rs {
		s += ptrSum(r)
	}
	return s
}

//go:noinline
func sumArrayByVal(rs [3]record) int64 {
	gcStress()
	var s int64
	for _, r := range rs {
		s += ptrSum(r)
	}
	return s
}

//go:noinline
func retSlice(i int64) []record {
	rs := make([]record, 0, 3)
	for k := int64(0); k < 3; k++ {
		rs = append(rs, mkRecord(i+k))
	}
	gcStress()
	return rs
}

//go:noinline
func retArray(i int64) [3]record {
	var rs [3]record
	for k := int64(0); k < 3; k++ {
		rs[k] = mkRecord(i + k)
	}
	gcStress()
	return rs
}

func wantSum(i int64, n int) int64 {
	var total int64
	for k := 0; k < n; k++ {
		j := i + int64(k)
		for p := int64(0); p < 6; p++ {
			total += j*16 + 1 + p
		}
	}
	return total
}

func main() {
	for i := int64(0); i < 16; i++ {
		r := mkRecord(i)
		gcStress()
		ck(checkRecord(r, i), fmt.Sprintf("original aggregate corrupted i=%d", i))

		ck(sumByVal(r) == wantSum(i, 1), fmt.Sprintf("byval struct sum i=%d", i))
		gcStress()
		ck(checkRecord(r, i), fmt.Sprintf("caller aggregate after byval call i=%d", i))

		ck(checkByVal(r, i), fmt.Sprintf("byval struct copy after GC i=%d", i))
		ck(checkByVal(retArray(i)[0], i), fmt.Sprintf("array element byval i=%d", i))

		rs := retSlice(i)
		gcStress()
		if len(rs) != 3 {
			ck(false, fmt.Sprintf("retSlice len=%d", len(rs)))
		} else {
			for k := range rs {
				ck(checkRecord(rs[k], i+int64(k)), fmt.Sprintf("retSlice[%d] corrupted i=%d", k, i))
			}
			ck(sumSliceByVal(rs) == wantSum(i, 3), fmt.Sprintf("byval slice sum i=%d", i))
			gcStress()
			for k := range rs {
				ck(checkRecord(rs[k], i+int64(k)), fmt.Sprintf("retSlice[%d] after byval call i=%d", k, i))
			}
		}

		arr := retArray(i)
		gcStress()
		for k := range arr {
			ck(checkRecord(arr[k], i+int64(k)), fmt.Sprintf("retArray[%d] corrupted i=%d", k, i))
		}
		ck(sumArrayByVal(arr) == wantSum(i, 3), fmt.Sprintf("byval array sum i=%d", i))
		gcStress()
		for k := range arr {
			ck(checkRecord(arr[k], i+int64(k)), fmt.Sprintf("retArray[%d] after byval call i=%d", k, i))
		}
	}

	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED l17_slice_struct_byval (%d failed checks)\n", n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS l17_slice_struct_byval")
}
