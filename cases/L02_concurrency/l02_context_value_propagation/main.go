// L02: context value 传播（grpc metadata / tailscale 请求上下文场景）：
// 值沿 parent→child 链可见、近端覆盖远端、兄弟分支互不可见、cancel/deadline
// 不改写已绑定的值；跨 goroutine 与 64 层深链传播，GC 后指针仍有效。
package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type key struct{ name string }

type blob struct {
	id  int
	v   int64
	tag string
}

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	gcMu.Lock()
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	gcMu.Unlock()
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

func seedVal() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return int64(n)
		}
	}
	return 1
}

func wantBlob(ctx context.Context, k key, id int, seed int64) {
	v, ok := ctx.Value(k).(*blob)
	if !ok || v == nil {
		ck(false, fmt.Sprintf("missing value for key %q", k.name))
		return
	}
	if v.id != id || v.v != seed*1000+int64(id) || v.tag != k.name {
		ck(false, fmt.Sprintf("value mismatch for key %q", k.name))
	}
}

func main() {
	seed := seedVal()
	keyA := key{"a"}
	keyB := key{"b"}
	keyC := key{"c"}
	type otherKey struct{ name string }

	root, cancelRoot := context.WithCancel(context.Background())
	defer cancelRoot()

	sib1 := context.WithValue(root, keyA, &blob{id: 1, v: seed*1000 + 1, tag: "a"})
	sib2 := context.WithValue(root, keyB, &blob{id: 2, v: seed*1000 + 2, tag: "b"})
	br0 := context.WithValue(root, keyA, &blob{id: 10, v: seed*1000 + 10, tag: "a"})
	br1 := context.WithValue(br0, keyB, &blob{id: 11, v: seed*1000 + 11, tag: "b"})
	br2 := context.WithValue(br1, keyA, &blob{id: 12, v: seed*1000 + 12, tag: "a"})

	ck(root.Value(keyA) == nil, "root must not see child value")
	ck(sib1.Value(keyB) == nil, "sibling 1 must not see sibling 2 value")
	ck(sib2.Value(keyA) == nil, "sibling 2 must not see sibling 1 value")
	wantBlob(sib1, keyA, 1, seed)
	wantBlob(sib2, keyB, 2, seed)
	wantBlob(br1, keyA, 10, seed)
	wantBlob(br1, keyB, 11, seed)
	wantBlob(br2, keyA, 12, seed)
	wantBlob(br2, keyB, 11, seed)
	wantBlob(br0, keyA, 10, seed)
	ck(br0.Value(keyB) == nil, "parent must not see value attached by child")

	// 不同 key 类型的同名 name 不冲突。
	ok := context.WithValue(root, otherKey{"a"}, &blob{id: 99, v: seed, tag: "other"})
	ck(ok.Value(keyA) == nil, "different key type must not collide")
	if v, _ := ok.Value(otherKey{"a"}).(*blob); v == nil || v.id != 99 {
		ck(false, "typed key lookup failed")
	}

	// WithValue 不改变 Done/Deadline 的传播。
	ck(br2.Done() == root.Done(), "WithValue must not replace Done channel")
	expired, cancelExp := context.WithDeadline(br2, time.Unix(0, 0))
	defer cancelExp()
	<-expired.Done()
	ck(expired.Err() == context.DeadlineExceeded, "expired deadline must be DeadlineExceeded")
	wantBlob(expired, keyA, 12, seed)
	wantBlob(expired, keyB, 11, seed)

	// nil key panic 必须可判读。
	func() {
		defer func() {
			if recover() == nil {
				ck(false, "WithValue with nil key must panic")
			}
		}()
		_ = context.WithValue(root, nil, 1)
	}()

	// nil value 合法，查找得到 nil 类型断言失败。
	nilVal := context.WithValue(root, keyC, (*blob)(nil))
	if v, ok := nilVal.Value(keyC).(*blob); !ok || v != nil {
		ck(false, "nil value must be stored and retrievable as typed nil")
	}

	// 64 层深链：近端覆盖 + 全链可见。
	deep := br2
	for i := 0; i < 64; i++ {
		deep = context.WithValue(deep, key{name: fmt.Sprintf("d%d", i)}, &blob{id: i, v: seed + int64(i), tag: "deep"})
	}
	for i := 0; i < 64; i++ {
		k := key{name: fmt.Sprintf("d%d", i)}
		v, ok := deep.Value(k).(*blob)
		if !ok || v == nil || v.id != i || v.v != seed+int64(i) || v.tag != "deep" {
			ck(false, fmt.Sprintf("deep value %d mismatch", i))
		}
	}
	wantBlob(deep, keyA, 12, seed)
	ck(br2.Value(key{"d63"}) == nil, "parent must not see deep child value")

	// 跨 goroutine：父 context 取消后，值仍可读且跨 GC 存活。
	var wg sync.WaitGroup
	var workers int32
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			atomic.AddInt32(&workers, 1)
			child := context.WithValue(deep, key{"worker"}, &blob{id: i, v: seed + int64(i)*7, tag: "worker"})
			gcStress()
			wantBlob(child, keyA, 12, seed)
			wantBlob(child, keyB, 11, seed)
			v, ok := child.Value(key{"worker"}).(*blob)
			if !ok || v == nil || v.id != i || v.v != seed+int64(i)*7 {
				ck(false, "worker-local value mismatch")
			}
			if deep.Value(key{"worker"}) != nil {
				ck(false, "worker-local value leaked to parent")
			}
			if child.Err() != context.Canceled {
				ck(false, "child must observe parent cancellation")
			}
		}(i)
	}
	cancelRoot()
	close(start)
	wg.Wait()
	ck(atomic.LoadInt32(&workers) == 8, fmt.Sprintf("workers=%d want=8", workers))
	ck(root.Err() == context.Canceled, "root not canceled")
	wantBlob(deep, keyA, 12, seed)
	wantBlob(deep, keyB, 11, seed)
	gcStress()
	if v, _ := deep.Value(keyA).(*blob); v == nil || v.id != 12 || v.v != seed*1000+12 || v.tag != "a" {
		ck(false, "value corrupted after parent cancel and GC")
	}

	fmt.Printf("depth=64 workers=8 shadow=2 expired_deadline=1 nil_key_panic=1\n")
	finish("l02_context_value_propagation")
}
