// L18: 新分配必须全零；FreeOSMemory 堆收缩后仍被引用的对象必须完好。
package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"
)

type node struct {
	v    int64
	name string
}

type kv struct {
	k string
	p *node
}

var failed int32

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
func allocNodes(n int) []*node { return make([]*node, n) }

//go:noinline
func allocKvs(n int) []kv { return make([]kv, n) }

func main() {
	ring := make([][]*node, 0, 2)
	ring2 := make([][]kv, 0, 2)

	for cycle := 0; cycle < 15; cycle++ {
		a := allocNodes(1024)
		for i := range a {
			if a[i] != nil {
				ck(false, "fresh []*node not zeroed")
				break
			}
		}
		for i := range a {
			a[i] = &node{v: int64(i), name: "z"}
		}

		b := allocKvs(256)
		for i := range b {
			if b[i].p != nil || b[i].k != "" {
				ck(false, "fresh []kv not zeroed")
				break
			}
		}
		for i := range b {
			b[i] = kv{k: "k", p: &node{v: int64(i + 1), name: "z"}}
		}

		runtime.GC()
		for i := 0; i < len(a); i += 97 {
			if a[i] == nil || a[i].v != int64(i) {
				ck(false, "allocated node corrupted")
				break
			}
		}
		for i := 0; i < len(b); i += 37 {
			if b[i].p == nil || b[i].p.v != int64(i+1) || b[i].k != "k" {
				ck(false, "allocated kv corrupted")
				break
			}
		}

		ring = append(ring, a)
		if len(ring) > 2 {
			ring = ring[1:]
		}
		ring2 = append(ring2, b)
		if len(ring2) > 2 {
			ring2 = ring2[1:]
		}
		if cycle%4 == 0 {
			debug.FreeOSMemory()
		}
	}

	for _, a := range ring {
		for i := 0; i < len(a); i += 211 {
			if a[i] == nil || a[i].v != int64(i) {
				ck(false, "survivor nodes corrupted after heap shrink")
				break
			}
		}
	}
	for _, b := range ring2 {
		for i := 0; i < len(b); i += 113 {
			if b[i].p == nil || b[i].p.v != int64(i+1) {
				ck(false, "survivor kv corrupted after heap shrink")
				break
			}
		}
	}

	big := allocNodes(32768)
	for i := range big {
		if big[i] != nil {
			ck(false, "fresh large []*node not zeroed")
			break
		}
	}
	for i := range big {
		big[i] = &node{v: int64(i), name: "big"}
	}
	runtime.GC()
	ck(big[0].v == 0 && big[32767].v == 32767, "large allocation after GC")
	runtime.KeepAlive(big)
	finish("l18_heap_zero_churn")
}
