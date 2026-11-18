// L11: 接口字段去虚拟化——struct 接口字段、嵌入接口、接口参数、
// map/slice 容器中的动态分派与具体类型直接调用必须等价；GC 后
// 仅由接口字段存活的实现对象仍完整。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type dvHandler interface {
	Handle(int64) (int64, int64)
	Kind() int64
}

type dvAdd struct{ k int64 }

func (a dvAdd) Handle(x int64) (int64, int64) { return x + a.k, x * a.k }
func (a dvAdd) Kind() int64                   { return 1 }

type dvMul struct{ k int64 }

func (m *dvMul) Handle(x int64) (int64, int64) { return x - m.k, x * m.k }
func (m *dvMul) Kind() int64                   { return 2 }

type dvXor struct{ k int64 }

func (v dvXor) Handle(x int64) (int64, int64) { return x ^ v.k, x + v.k }
func (v dvXor) Kind() int64                   { return 3 }

type dvPipe struct {
	h    dvHandler
	next *dvPipe
	tag  string
}

func (p *dvPipe) run(x int64) (int64, int64) { return p.h.Handle(x) }

type dvEmbed struct {
	dvHandler
}

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func dvDirect(h dvHandler, x int64) (int64, int64) {
	switch v := h.(type) {
	case dvAdd:
		return v.Handle(x)
	case *dvMul:
		return v.Handle(x)
	case dvXor:
		return v.Handle(x)
	}
	return -1, -1
}

func dvCall(h dvHandler, x int64) (int64, int64) { return h.Handle(x) }

func dvMake(i int, k int64) dvHandler {
	switch i % 3 {
	case 0:
		return dvAdd{k: k}
	case 1:
		return &dvMul{k: k}
	default:
		return dvXor{k: k}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func dvRecover(fn func()) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = fmt.Sprint(r)
		}
	}()
	fn()
	return "ok"
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	n := 36 + int(seed%20)
	x0 := int64(seed%17) - 8

	hs := make([]dvHandler, n)
	pipes := make([]*dvPipe, n)
	m := make(map[string]dvHandler, n)
	for i := 0; i < n; i++ {
		k := int64(i%9) - 4
		h := dvMake(i, k)
		hs[i] = h
		pipes[i] = &dvPipe{h: h, tag: fmt.Sprintf("p%d", i)}
		if i > 0 {
			pipes[i].next = pipes[i-1]
		}
		m["k"+strconv.Itoa(i)] = h
	}

	gcStress()

	ok := true
	for i := 0; i < n; i++ {
		x := x0 + int64(i)
		vw, vh := pipes[i].h.Handle(x)
		dw, dh := dvDirect(hs[i], x)
		if vw != dw || vh != dh {
			ok = false
			break
		}
		if pipes[i].h.Kind() != hs[i].Kind() {
			ok = false
			break
		}
		rw, rh := pipes[i].run(x)
		if rw != dw || rh != dh {
			ok = false
			break
		}
		cw, ch := dvCall(hs[i], x)
		if cw != dw || ch != dh {
			ok = false
			break
		}
	}
	ck(ok, "interface field vs concrete dispatch")

	ck(pipes[n-1].next != nil && portsTagOK(pipes), "pipe chain retained through GC")

	// 字段重新绑定后必须看到新实现。
	oldH := pipes[0].h
	ri := 1
	for ri < n && hs[ri].Kind() == oldH.Kind() {
		ri++
	}
	if ri == n {
		ri = n - 1
	}
	pipes[0].h = hs[ri]
	ck(pipes[0].h.Kind() == hs[ri].Kind(), "interface field rebinding")
	w0, h0 := pipes[0].run(x0)
	dw0, dh0 := dvDirect(hs[ri], x0)
	ck(w0 == dw0 && h0 == dh0, "rebound interface field dispatch")

	// 嵌入接口的提升方法。
	emb := &dvEmbed{dvHandler: hs[0]}
	embW, embH := emb.Handle(x0)
	dirW, dirH := dvDirect(hs[0], x0)
	ck(embW == dirW && embH == dirH, "embedded interface promotion")

	// 容器中的接口值分派。
	var mapSum, wantSum int64
	for i := 0; i < n; i++ {
		w, h := m["k"+strconv.Itoa(i)].Handle(x0)
		mapSum += w*3 + h
		dw, dh := dvDirect(hs[i], x0)
		wantSum += dw*3 + dh
	}
	ck(mapSum == wantSum, "map interface dispatch")

	var sliceSum, wantSlice int64
	for i := 0; i < n; i++ {
		w, h := hs[i].Handle(x0 + 1)
		sliceSum += w + h
		dw, dh := dvDirect(hs[i], x0+1)
		wantSlice += dw + dh
	}
	ck(sliceSum == wantSlice, "slice interface dispatch")

	// 类型断言取回具体实现并直接调用。
	assertOK := true
	for i := 0; i < n; i++ {
		switch v := hs[i].(type) {
		case dvAdd:
			w, h := v.Handle(x0)
			dw, dh := dvDirect(hs[i], x0)
			if w != dw || h != dh {
				assertOK = false
			}
		case *dvMul:
			w, h := v.Handle(x0)
			dw, dh := dvDirect(hs[i], x0)
			if w != dw || h != dh {
				assertOK = false
			}
		case dvXor:
			w, h := v.Handle(x0)
			dw, dh := dvDirect(hs[i], x0)
			if w != dw || h != dh {
				assertOK = false
			}
		}
	}
	ck(assertOK, "type assertion to concrete dispatch")

	// nil 接口字段调用必须 panic。
	nilPipe := &dvPipe{tag: "nil"}
	ck(nilPipe.h == nil, "zero interface field is nil")
	out := dvRecover(func() { nilPipe.run(3) })
	ck(contains(out, "nil pointer dereference"), "nil interface field call panics")

	gcStress()
	ok = true
	for i := 0; i < n; i++ {
		if pipes[i].tag == "" || pipes[i].h == nil || hs[i] == nil {
			ok = false
			break
		}
	}
	ck(ok, "interface implementations after final GC")

	finish("l11_devirt_iface_field")
}

func portsTagOK(pipes []*dvPipe) bool {
	for _, p := range pipes {
		if p.tag == "" {
			return false
		}
	}
	return true
}
