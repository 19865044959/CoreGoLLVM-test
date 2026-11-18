// L01: 泛型错误包装/错误链：Wrap[T]/Find[T] + fmt.Errorf("%w") + errors.Is/As/Join 跨 GC。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type payload struct {
	code int64
	note string
}

var ErrBase = errors.New("base failure")

type gerr[T comparable] struct {
	code  T
	op    string
	cause error
	data  *payload
}

func (e *gerr[T]) Error() string {
	if e.cause == nil {
		return e.op
	}
	return e.op + ": " + e.cause.Error()
}

func (e *gerr[T]) Unwrap() error { return e.cause }

func (e *gerr[T]) Is(target error) bool {
	t, ok := target.(*gerr[T])
	return ok && t.code == e.code
}

func Wrap[T comparable](code T, op string, cause error, data *payload) error {
	return &gerr[T]{code: code, op: op, cause: cause, data: data}
}

func Find[T comparable](err error) (T, *payload, bool) {
	var zero T
	var target *gerr[T]
	if errors.As(err, &target) {
		return target.code, target.data, true
	}
	return zero, nil, false
}

var (
	failed  int32
	gcSink  [][]byte
	errSink []error
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := 40 + int(uafSeed%11)

	for r := 0; r < rounds; r++ {
		pInt := &payload{code: 404, note: fmt.Sprintf("int-%d", r)}
		pStr := &payload{code: 7, note: fmt.Sprintf("str-%d", r)}

		base := Wrap(int64(404), "db", ErrBase, pInt)
		mid := fmt.Errorf("sql: %w", base)
		mid = fmt.Errorf("outer: %w", mid)
		top := Wrap("E_API", "api", mid, pStr)
		joined := errors.Join(top, fmt.Errorf("tail: %w", ErrBase))

		errSink = append(errSink[:0], base, mid, top, joined)
		gcStress()

		ck(errors.Is(joined, ErrBase), "joined lost sentinel base error")
		ck(errors.Is(joined, &gerr[int64]{code: 404}), "custom Is[int64] did not match")
		ck(!errors.Is(joined, &gerr[int64]{code: 500}), "custom Is[int64] matched wrong code")
		ck(errors.Is(joined, &gerr[string]{code: "E_API"}), "custom Is[string] did not match")
		ck(!errors.Is(joined, &gerr[string]{code: "OTHER"}), "custom Is[string] matched wrong code")
		ck(!errors.Is(joined, nil), "errors.Is(joined, nil) should be false")

		c1, d1, ok := Find[int64](joined)
		ck(ok && c1 == 404, "Find[int64] failed after GC")
		ck(d1 == pInt, "Find[int64] payload pointer identity changed")
		ck(d1 != nil && d1.code == 404 && d1.note == fmt.Sprintf("int-%d", r), "Find[int64] payload corrupted")

		c2, d2, ok := Find[string](joined)
		ck(ok && c2 == "E_API", "Find[string] failed after GC")
		ck(d2 == pStr, "Find[string] payload pointer identity changed")
		ck(d2 != nil && d2.code == 7 && d2.note == fmt.Sprintf("str-%d", r), "Find[string] payload corrupted")

		c3, d3, ok := Find[int32](joined)
		ck(!ok && c3 == 0 && d3 == nil, "Find[int32] unexpectedly matched")

		wantText := "api: outer: sql: db: base failure"
		ck(top.Error() == wantText, "error text corrupted after GC")

		depth := 0
		for e := top; e != nil; e = errors.Unwrap(e) {
			depth++
		}
		ck(depth == 5, fmt.Sprintf("unwrap depth got=%d want=5", depth))
		ck(errors.Unwrap(top) == mid, "first unwrap identity changed")

		// 未包装的泛型错误：Unwrap 为 nil，Is 仍按 code 匹配。
		solo := Wrap(int64(9), "solo", nil, nil)
		gcStress()
		ck(errors.Is(solo, &gerr[int64]{code: 9}), "solo custom Is failed")
		ck(!errors.Is(solo, ErrBase), "solo matched unrelated sentinel")
		ck(errors.Unwrap(solo) == nil, "solo unwrap should be nil")
	}

	ck(errors.Is(nil, nil), "errors.Is(nil, nil) should be true")
	ck(!errors.Is(ErrBase, nil), "errors.Is(non-nil, nil) should be false")

	finish("l01_generic_error")
}
