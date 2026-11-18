// L11: nil 接口与 typed nil 的方法分派——nil 接口、装有不安全 nil 指针的
// 接口、nil-safe 接收者、嵌入接口、typed nil error（errors.As/Is）以及
// nil func 装箱后的调用/断言边界必须在 GC 前后一致。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ndIface interface {
	Val() int64
	Name() string
}

type ndImpl struct{ k int64 }

func (d *ndImpl) Val() int64 {
	if d == nil {
		return -7
	}
	return d.k
}

func (d *ndImpl) Name() string {
	if d == nil {
		return "nil-impl"
	}
	return "impl"
}

type ndStrict struct{ k int64 }

func (d *ndStrict) Val() int64   { return d.k }
func (d *ndStrict) Name() string { return "strict" }

type ndVal struct{ k int64 }

func (d ndVal) Val() int64   { return d.k }
func (d ndVal) Name() string { return "val" }

type ndEmbed struct {
	ndIface
}

type ndHolder struct {
	h   ndIface
	tag string
}

type ndErr struct{ msg string }

func (e *ndErr) Error() string {
	if e == nil {
		return "nil-error"
	}
	return e.msg
}

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
	ndKeep []ndIface
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

func ndCall(v ndIface) (out int64, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	return v.Val(), false
}

func ndCallName(v ndIface) (out string, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	return v.Name(), false
}

func ndCallAny(fn func()) (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	fn()
	return false
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	var nilI ndIface
	ck(nilI == nil, "zero interface is nil")
	if _, panicked := ndCall(nilI); !panicked {
		ck(false, "nil interface method call must panic")
	}
	if _, panicked := ndCallName(nilI); !panicked {
		ck(false, "nil interface name call must panic")
	}

	var np *ndImpl
	var safe ndIface = np
	ck(safe != nil, "typed nil safe impl interface is not nil")
	if v, panicked := ndCall(safe); panicked || v != -7 {
		ck(false, "typed nil safe receiver method")
	}
	if n, panicked := ndCallName(safe); panicked || n != "nil-impl" {
		ck(false, "typed nil safe receiver name")
	}
	q, ok := safe.(*ndImpl)
	ck(ok && q == nil, "typed nil assertion yields nil pointer")

	var ns *ndStrict
	var strict ndIface = ns
	ck(strict != nil, "typed nil strict interface is not nil")
	if _, panicked := ndCall(strict); !panicked {
		ck(false, "typed nil strict receiver must panic")
	}
	if n, panicked := ndCallName(strict); panicked || n != "strict" {
		ck(false, "typed nil strict name without deref")
	}

	var vv ndIface = ndVal{k: int64(seed%17) + 5}
	wantVal := int64(seed%17) + 5
	if v, panicked := ndCall(vv); panicked || v != wantVal {
		ck(false, "value implementation dispatch")
	}

	var a ndIface = (*ndImpl)(nil)
	var b ndIface = np
	var c ndIface = (*ndStrict)(nil)
	ck(a == b, "same typed nil interfaces compare equal")
	ck(a != c, "different typed nil interfaces differ")

	tnil := 0
	tdyn := 0
	switch v := nilI.(type) {
	case nil:
		tnil++
	default:
		_ = v
		tdyn++
	}
	ck(tnil == 1 && tdyn == 0, "type switch nil interface")
	tsOK := false
	switch v := safe.(type) {
	case *ndImpl:
		tsOK = v == nil
	}
	ck(tsOK, "type switch typed nil case")

	holder := &ndHolder{tag: "h"}
	ck(holder.h == nil, "zero interface field is nil")
	if _, panicked := ndCall(holder.h); !panicked {
		ck(false, "nil interface field call must panic")
	}
	holder.h = safe
	if v, panicked := ndCall(holder.h); panicked || v != -7 {
		ck(false, "interface field typed nil dispatch")
	}
	holder.h = ndVal{k: 9}
	if v, panicked := ndCall(holder.h); panicked || v != 9 {
		ck(false, "interface field value reassignment")
	}
	holder.h = nil
	ck(holder.h == nil, "interface field reset to nil")

	emb := &ndEmbed{}
	ck(emb.ndIface == nil, "zero embedded interface is nil")
	if _, panicked := ndCall(emb); !panicked {
		ck(false, "nil embedded interface call must panic")
	}
	emb.ndIface = np
	if v, panicked := ndCall(emb); panicked || v != -7 {
		ck(false, "embedded typed nil interface dispatch")
	}

	var fn func() int64
	var av any = fn
	ck(av != nil, "typed nil func boxed in any is not nil")
	gf, fok := av.(func() int64)
	ck(fok && gf == nil, "typed nil func assertion")
	if !ndCallAny(func() { _ = gf() }) {
		ck(false, "calling typed nil func must panic")
	}

	var ep *ndErr
	var e error = ep
	ck(e != nil, "typed nil error interface is not nil")
	ck(e.Error() == "nil-error", "typed nil error nil-safe method")
	var target *ndErr
	ck(errors.As(e, &target), "errors.As typed nil error")
	ck(target == nil, "errors.As typed nil target pointer")
	wrapped := fmt.Errorf("wrap: %w", e)
	ck(wrapped != nil, "wrapped typed nil error is not nil")
	ck(errors.Is(wrapped, e), "errors.Is wrapped typed nil error")
	var target2 *ndErr
	ck(errors.As(wrapped, &target2), "errors.As wrapped typed nil error")
	ck(target2 == nil, "errors.As wrapped target pointer")

	var nilErr error
	ck(nilErr == nil, "zero error is nil")
	if !ndCallAny(func() { _ = nilErr.Error() }) {
		ck(false, "nil error method call must panic")
	}

	var sl []int
	var sv any = sl
	ck(sv != nil, "typed nil slice in any is not nil")
	gs, sok := sv.([]int)
	ck(sok && gs == nil && len(gs) == 0, "typed nil slice assertion")
	ck(append(gs, 3) != nil, "append to typed nil slice allocates")

	ndKeep = append(ndKeep, safe, vv, &ndImpl{k: 3}, ndVal{k: 4})
	gcStress()
	ck(safe != nil && strict != nil && a != nil && b != nil && c != nil, "typed nil interfaces after GC")
	if v, panicked := ndCall(safe); panicked || v != -7 {
		ck(false, "typed nil safe receiver after GC")
	}
	if _, panicked := ndCall(strict); !panicked {
		ck(false, "typed nil strict receiver after GC")
	}
	ck(e != nil && e.Error() == "nil-error" && errors.Is(wrapped, e), "typed nil error after GC")

	keepOK := true
	for _, k := range ndKeep {
		if k == nil {
			keepOK = false
			break
		}
		if _, panicked := ndCall(k); panicked {
			keepOK = false
			break
		}
	}
	ck(keepOK, "retained interface implementations after GC")

	finish("l11_nil_iface_dispatch")
}
