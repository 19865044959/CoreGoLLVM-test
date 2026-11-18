// L07: []**node 槽位操作、指针算术与 atomic 槽位替换，GC 后嵌套指针链仍有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type node struct {
	v    int64
	name string
	next *node
}

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 8192)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// 槽位指向 arr 的元素；arr 的唯一引用就是这些内部-ish 槽位指针。
//
//go:noinline
func buildSlots(n int) []**node {
	arr := make([]*node, n)
	slots := make([]**node, n)
	for i := 0; i < n; i++ {
		arr[i] = &node{
			v:    int64(i),
			name: "sn-" + strconv.Itoa(i),
			next: &node{v: int64(i) * 2, name: "nn-" + strconv.Itoa(i)},
		}
		slots[i] = &arr[i]
	}
	return slots
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	rounds := 64
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			rounds = v
		}
	}

	for r := 0; r < rounds; r++ {
		n := 8 + int((seed+int64(r))%8)
		slots := buildSlots(n)
		gcStress()

		for i := 0; i < n; i++ {
			sp := slots[i]
			if sp == nil || *sp == nil {
				ck(false, fmt.Sprintf("round %d: slot %d dangling", r, i))
				break
			}
			nd := *sp
			if nd.v != int64(i) || nd.name != "sn-"+strconv.Itoa(i) {
				ck(false, fmt.Sprintf("round %d: slot %d node corrupted", r, i))
				break
			}
			if nd.next == nil || nd.next.v != int64(i)*2 || nd.next.name != "nn-"+strconv.Itoa(i) {
				ck(false, fmt.Sprintf("round %d: slot %d next corrupted", r, i))
				break
			}
		}

		// 单表达式指针算术定位槽位。
		k := int((seed + int64(r)) % int64(n))
		base := unsafe.Pointer(&slots[0])
		got := (***node)(unsafe.Add(base, uintptr(k)*unsafe.Sizeof(slots[0])))
		ck(got == &slots[k], fmt.Sprintf("round %d: pointer arithmetic slot", r))
		ck((**got) != nil && (**got).v == int64(k), fmt.Sprintf("round %d: pointer arithmetic value", r))
		// atomic 换入新节点，旧节点由类型化局部变量保活。
		newNode := &node{v: 900 + int64(k), name: "atomic-node", next: &node{v: 901, name: "atomic-next"}}
		oldPtr := unsafe.Pointer(*slots[k])
		slotAddr := (*unsafe.Pointer)(unsafe.Pointer(slots[k]))
		atomic.StorePointer(slotAddr, unsafe.Pointer(newNode))
		ck(atomic.LoadPointer(slotAddr) == unsafe.Pointer(newNode),
			fmt.Sprintf("round %d: atomic load new node", r))
		gcStress()
		ck((*slots[k]) == newNode && (*slots[k]).v == 900+int64(k), fmt.Sprintf("round %d: atomic new node after GC", r))
		oldNode := (*node)(oldPtr)
		ck(oldNode != nil && oldNode.v == int64(k) && oldNode.name == "sn-"+strconv.Itoa(k),
			fmt.Sprintf("round %d: displaced slot node survives GC", r))

		// CAS 换回原节点。
		ok := atomic.CompareAndSwapPointer(slotAddr, unsafe.Pointer(newNode), oldPtr)
		ck(ok, fmt.Sprintf("round %d: CAS swap back", r))
		ck(unsafe.Pointer(*slots[k]) == oldPtr, fmt.Sprintf("round %d: CAS result", r))
		gcStress()
		restored := *slots[k]
		if restored == nil || restored.v != int64(k) || restored.next == nil || restored.next.v != int64(k)*2 {
			ck(false, fmt.Sprintf("round %d: restored slot corrupted", r))
		}

		// 槽位子切片视图。
		lo := n / 3
		hi := lo + n/2
		if hi > n {
			hi = n
		}
		block := slots[lo:hi]
		ck(len(block) == hi-lo && block[0] == slots[lo] && block[len(block)-1] == slots[hi-1],
			fmt.Sprintf("round %d: slot block view", r))

		slots = nil
		block = nil
		oldNode = nil
		oldPtr = nil
		gcStress()
	}

	finish("l07_pointer_array_slots")
}
