// L18: runtime.KeepAlive 多对象边界。
//  1. 切片整体边界：一批带 finalizer 的对象最后一次字段读取后执行 GC+轮询，
//     KeepAlive(objs) 位于函数末尾；
//  2. 接口边界：对象只通过 interface{} 可达，GC 后 KeepAlive(v) 前不得终结；
//  3. 循环边界：每轮对象在末次使用后 GC，KeepAlive 在迭代尾部。
//
// finalizer 只写标记，不读对象内容；只断言“可达不终结 / 失活最终终结”。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

const (
	kaObjN  = 8
	kaDataN = 48
)

type kaObj struct {
	id   int32
	v    int64
	name string
	data [kaDataN]byte
}

var (
	failed  int32
	kaFlags []int32
	kaNext  int32
	kaSink  int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

//go:noinline
func newKAObj() *kaObj {
	id := atomic.AddInt32(&kaNext, 1)
	o := &kaObj{id: id, v: int64(id) * 13, name: "ka"}
	for i := range o.data {
		o.data[i] = byte((int(id)*7 + i*3) % 251)
	}
	runtime.SetFinalizer(o, func(q *kaObj) {
		idx := int(q.id)
		if idx > 0 && idx < len(kaFlags) {
			atomic.StoreInt32(&kaFlags[idx], 1)
		}
	})
	return o
}

func finalizedAmong(ids []int32) int {
	n := 0
	for _, id := range ids {
		if atomic.LoadInt32(&kaFlags[id]) != 0 {
			n++
		}
	}
	return n
}

func pendingAmong(ids []int32) int {
	n := 0
	for _, id := range ids {
		if atomic.LoadInt32(&kaFlags[id]) == 0 {
			n++
		}
	}
	return n
}

//go:noinline
func boundarySlice(n int) []int32 {
	objs := make([]*kaObj, n)
	ids := make([]int32, n)
	for i := 0; i < n; i++ {
		o := newKAObj()
		objs[i] = o
		ids[i] = o.id
		kaSink += o.v + int64(len(o.name)) + int64(o.data[0]) + int64(o.data[kaDataN-1])
	}

	// 最后一次字段读取之后、KeepAlive 之前：对象必须保持存活。
	for i := 0; i < 20 && finalizedAmong(ids) == 0; i++ {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(finalizedAmong(ids) == 0, fmt.Sprintf("%d slice-held objects finalized before KeepAlive boundary", finalizedAmong(ids)))
	runtime.KeepAlive(objs)
	ck(finalizedAmong(ids) == 0, "slice-held object finalized at KeepAlive boundary")
	return ids
}

//go:noinline
func boundaryIface() int32 {
	var v interface{} = newKAObj()
	o := v.(*kaObj)
	id := o.id
	kaSink += o.v + int64(len(o.name)) + int64(o.data[1])

	for i := 0; i < 20 && atomic.LoadInt32(&kaFlags[id]) == 0; i++ {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(atomic.LoadInt32(&kaFlags[id]) == 0, "interface-held object finalized before KeepAlive boundary")
	runtime.KeepAlive(v)
	ck(atomic.LoadInt32(&kaFlags[id]) == 0, "interface-held object finalized at KeepAlive boundary")
	return id
}

//go:noinline
func boundaryLoop(n int) []int32 {
	ids := make([]int32, 0, n)
	for i := 0; i < n; i++ {
		o := newKAObj()
		ids = append(ids, o.id)
		kaSink += o.v + int64(o.data[2])

		runtime.GC()
		runtime.GC()
		ck(atomic.LoadInt32(&kaFlags[o.id]) == 0, fmt.Sprintf("loop object %d finalized before KeepAlive", o.id))
		runtime.KeepAlive(o)
	}
	return ids
}

func main() {
	kaFlags = make([]int32, 1<<15)

	all := boundarySlice(kaObjN)
	ifaceID := boundaryIface()
	all = append(all, ifaceID)
	all = append(all, boundaryLoop(4)...)

	// 所有边界函数返回后对象已失活，最终必须全部被终结。
	deadline := time.Now().Add(5 * time.Second)
	for pendingAmong(all) != 0 && time.Now().Before(deadline) {
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	ck(pendingAmong(all) == 0, fmt.Sprintf("%d objects not finalized after KeepAlive boundaries", pendingAmong(all)))
	finish("l18_keepalive_multi_objects")
}
