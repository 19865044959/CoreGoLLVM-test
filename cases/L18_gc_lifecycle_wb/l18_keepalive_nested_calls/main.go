// L18: KeepAlive 跨三层 noinline 调用链、interface 包装与 defer 边界。
// 对象只在边界函数内部创建，最后一次字段使用后进入多轮 GC 窗口，窗口末尾
// 读取 finalizer 标记（必须为 0），再执行 KeepAlive。函数返回后对象失活，
// 最终必须被终结。finalizer 只写原子标记。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	knPerVariant = 5
	knObjPerCall = 3
	knDataN      = 32
	knFlagCap    = 512
)

type knObj struct {
	id   int32
	data [knDataN]byte
}

var (
	failed  int32
	knFlags []int32
	knNext  int32
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

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func knByte(id int32, i int) byte { return byte((int(id)*47 + i*23) % 251) }

//go:noinline
func knNew() *knObj {
	id := atomic.AddInt32(&knNext, 1)
	o := &knObj{id: id}
	for i := range o.data {
		o.data[i] = knByte(id, i)
	}
	runtime.SetFinalizer(o, func(q *knObj) {
		if q.id > 0 && int(q.id) < len(knFlags) {
			atomic.StoreInt32(&knFlags[q.id], 1)
		}
	})
	return o
}

//go:noinline
func knUse(o *knObj) int64 {
	return int64(o.id) + int64(o.data[0]) + int64(o.data[knDataN-1])
}

//go:noinline
func knLevel3(o *knObj) int64 { return knUse(o) }

//go:noinline
func knLevel2(o *knObj) int64 { return knLevel3(o) }

// knGCWindow 在“最后一次使用”与 KeepAlive 之间制造多轮 GC 与 finalizer 窗口。
//
//go:noinline
func knGCWindow(rounds int) {
	for i := 0; i < rounds; i++ {
		b := make([]byte, 4096)
		b[i%len(b)] = byte(i)
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
	runtime.GC()
}

//go:noinline
func knNestedOnce(rounds int) (ids []int32, bad int32) {
	objs := [knObjPerCall]*knObj{}
	for i := range objs {
		objs[i] = knNew()
		ids = append(ids, objs[i].id)
	}
	var acc int64
	for _, o := range objs {
		acc += knLevel2(o)
	}
	knGCWindow(rounds)
	for _, id := range ids {
		if atomic.LoadInt32(&knFlags[id]) != 0 {
			bad++
		}
	}
	for _, o := range objs {
		runtime.KeepAlive(o)
	}
	_ = acc
	return ids, bad
}

//go:noinline
func knIfaceOnce(rounds int) (ids []int32, bad int32) {
	objs := [knObjPerCall]*knObj{}
	ifaces := [knObjPerCall]interface{}{}
	for i := range objs {
		objs[i] = knNew()
		ifaces[i] = objs[i]
		ids = append(ids, objs[i].id)
	}
	var acc int64
	for i := range ifaces {
		o := ifaces[i].(*knObj)
		acc += knUse(o)
	}
	knGCWindow(rounds)
	for _, id := range ids {
		if atomic.LoadInt32(&knFlags[id]) != 0 {
			bad++
		}
	}
	for i := range ifaces {
		runtime.KeepAlive(ifaces[i])
	}
	_ = acc
	return ids, bad
}

//go:noinline
func knDeferOnce(rounds int) (ids []int32, bad int32) {
	objs := [knObjPerCall]*knObj{}
	for i := range objs {
		objs[i] = knNew()
		ids = append(ids, objs[i].id)
		defer runtime.KeepAlive(objs[i])
	}
	var acc int64
	for _, o := range objs {
		acc += knUse(o)
	}
	knGCWindow(rounds)
	for _, id := range ids {
		if atomic.LoadInt32(&knFlags[id]) != 0 {
			bad++
		}
	}
	_ = acc
	return ids, bad
}

func knWaitFinalized(ids []int32, timeout time.Duration) int {
	deadline := time.Now().Add(timeout)
	for {
		pending := 0
		for _, id := range ids {
			if atomic.LoadInt32(&knFlags[id]) == 0 {
				pending++
			}
		}
		if pending == 0 || time.Now().After(deadline) {
			return pending
		}
		runtime.GC()
		runtime.Gosched()
		time.Sleep(time.Millisecond)
	}
}

func main() {
	seed := seedValue()
	rounds := 3 + int(seed%3)
	knFlags = make([]int32, knFlagCap)

	var allIDs []int32
	bad := int32(0)
	for i := 0; i < knPerVariant; i++ {
		ids, b := knNestedOnce(rounds)
		allIDs = append(allIDs, ids...)
		bad += b
		ids, b = knIfaceOnce(rounds)
		allIDs = append(allIDs, ids...)
		bad += b
		ids, b = knDeferOnce(rounds)
		allIDs = append(allIDs, ids...)
		bad += b
	}
	ck(bad == 0, fmt.Sprintf("objects finalized before KeepAlive boundary: %d (seed=%d)", bad, seed))

	pending := knWaitFinalized(allIDs, 5*time.Second)
	ck(pending == 0, fmt.Sprintf("objects not finalized after boundary returned: %d/%d (seed=%d)", pending, len(allIDs), seed))

	finish("l18_keepalive_nested_calls")
}
