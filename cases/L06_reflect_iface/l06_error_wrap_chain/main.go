// L06: 自定义 error 持指针、fmt.Errorf("%w") 链、errors.Is/As、GC 后解包。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type errCtx struct {
	Code   int64
	Detail *node
}

type ptrError struct {
	ctx *errCtx
	msg string
}

func (e *ptrError) Error() string { return e.msg + ":" + e.ctx.Detail.Label }
func (e *ptrError) Unwrap() error { return nil }

type codedError struct {
	Code  int64
	Op    string
	cause error
	ctx   *errCtx
}

func (e *codedError) Error() string {
	if e.cause == nil {
		return e.Op
	}
	return e.Op + ": " + e.cause.Error()
}

func (e *codedError) Unwrap() error { return e.cause }

func (e *codedError) Is(target error) bool {
	t, ok := target.(*codedError)
	return ok && t.Code == e.Code
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
	const rounds = 120
	for r := 0; r < rounds; r++ {
		layers := 4 + int((uafSeed+int64(r))%5)
		detailV := 11 + int64(r%3)
		base := &ptrError{
			ctx: &errCtx{Code: 7, Detail: &node{V: detailV, Label: "db"}},
			msg: "db",
		}
		var err error = base
		for i := 0; i < layers; i++ {
			err = fmt.Errorf("layer%d: %w", i, err)
		}
		code := &codedError{
			Code:  404,
			Op:    "api",
			cause: err,
			ctx:   &errCtx{Code: 404, Detail: &node{V: 99, Label: "api"}},
		}
		err = fmt.Errorf("outer: %w", code)
		errSink = append(errSink[:0], err, base, code)
		gcStress()

		ck(errors.Is(err, base), "errors.Is base pointer")
		ck(errors.Is(err, &codedError{Code: 404, Op: "probe"}), "errors.Is custom Is")
		ck(!errors.Is(err, &codedError{Code: 500, Op: "probe"}), "errors.Is custom Is mismatch")
		ck(!errors.Is(err, nil), "errors.Is nil target")

		var pe *ptrError
		ck(errors.As(err, &pe) && pe == base, "errors.As ptrError identity")
		ck(pe != nil && pe.ctx != nil && pe.ctx.Detail != nil, "errors.As payload pointers after GC")
		ck(pe.ctx.Detail.V == detailV && pe.ctx.Detail.Label == "db", "errors.As payload value after GC")
		ck(pe.ctx.Code == 7, "errors.As ctx code after GC")

		var ce *codedError
		ck(errors.As(err, &ce) && ce.Code == 404, "errors.As codedError")
		ck(ce.ctx != nil && ce.ctx.Detail != nil && ce.ctx.Detail.V == 99, "errors.As coded payload after GC")

		n := 0
		for e := err; e != nil; e = errors.Unwrap(e) {
			n++
		}
		ck(n == layers+3, "unwrap chain length") // outer + coded + layers + base

		want := "db:db"
		for i := 0; i < layers; i++ {
			want = fmt.Sprintf("layer%d: %s", i, want)
		}
		want = "api: " + want
		want = "outer: " + want
		ck(err.Error() == want, "error text after GC")
		ck(ce.Error() == want[len("outer: "):], "inner error text after GC")

		joined := errors.Join(fmt.Errorf("j1: %w", base), fmt.Errorf("j2: %w", code))
		errSink = append(errSink, joined)
		gcStress()
		ck(errors.Is(joined, base), "errors.Join Is base")
		ck(errors.Is(joined, &codedError{Code: 404, Op: "probe"}), "errors.Join Is custom")
		var pe2 *ptrError
		ck(errors.As(joined, &pe2) && pe2.ctx.Detail.V == detailV, "errors.Join As after GC")
		var ce2 *codedError
		ck(errors.As(joined, &ce2) && ce2.Code == 404 && ce2.ctx.Detail.V == 99, "errors.Join As coded after GC")
	}
	ck(errors.Is(nil, nil), "errors.Is nil nil")
	finish("l06_error_wrap_chain")
}
