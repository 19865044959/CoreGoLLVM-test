// L01: 泛型 pipeline：批量命令队列 + 通道级联流水线，失败短路，跨 GC 校验中间指针。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type rec struct {
	id    int64
	label string
	blob  []byte
}

// Pipeline[T] 是 pgx/go-redis pipeline 的泛型形态：先提交命令，再一次 Flush。
type Pipeline[T any] struct {
	names []string
	fns   []func() (*T, error)
}

func NewPipeline[T any]() *Pipeline[T] { return &Pipeline[T]{} }

func (p *Pipeline[T]) Submit(name string, fn func() (*T, error)) {
	p.names = append(p.names, name)
	p.fns = append(p.fns, fn)
}

func (p *Pipeline[T]) Len() int { return len(p.fns) }

// Flush 顺序执行，遇错短路并返回已完成的中间结果。
func (p *Pipeline[T]) Flush() ([]*T, error) {
	out := make([]*T, 0, len(p.fns))
	for i, fn := range p.fns {
		v, err := fn()
		if err != nil {
			return out, fmt.Errorf("op %s: %w", p.names[i], err)
		}
		out = append(out, v)
	}
	return out, nil
}

var errBoom = errors.New("boom")

// stage 是泛型流水线的一级：单 goroutine 保序，输入关闭后关闭输出。
func stage[T any](in <-chan *T, fn func(*T) *T) <-chan *T {
	out := make(chan *T, 1)
	go func() {
		defer close(out)
		for v := range in {
			out <- fn(v)
		}
	}()
	return out
}

var (
	failed  int32
	gcSink  [][]byte
	recSink []*rec
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 32 {
		n = 32
	}
	return n
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(1)
	n := 256 * rounds

	items := make([]*rec, n)
	for i := range items {
		items[i] = &rec{id: int64(i), label: fmt.Sprintf("i%d", i), blob: make([]byte, 8+i%6)}
		items[i].blob[0] = byte(i)
		items[i].blob[len(items[i].blob)-1] = byte(i * 7)
	}
	recSink = items

	// 批量命令：每条命令作用于一个 item，返回同一指针。
	batch := NewPipeline[rec]()
	for i := 0; i < n; i++ {
		it := items[i]
		step := int64(i%3 + 1)
		batch.Submit(fmt.Sprintf("inc-%d", i), func() (*rec, error) {
			it.id += step
			it.label += "-g"
			return it, nil
		})
	}
	ck(batch.Len() == n, "pipeline length corrupted")
	gcStress() // 命令闭包持有全部 item 指针，必须跨 GC 存活
	results, err := batch.Flush()
	ck(err == nil, "pipeline flush returned unexpected error")
	ck(len(results) == n, fmt.Sprintf("pipeline results got=%d want=%d", len(results), n))
	for i := 0; i < n; i++ {
		ck(results[i] == items[i], fmt.Sprintf("pipeline result pointer mismatch at %d", i))
		wantID := int64(i + i%3 + 1)
		ck(items[i].id == wantID, fmt.Sprintf("pipeline payload id corrupted at %d", i))
		ck(items[i].label == fmt.Sprintf("i%d-g", i), "pipeline label corrupted")
	}

	// 失败短路：第 5 条出错，之后命令不得执行。
	pre := make([]*rec, 8)
	for i := range pre {
		pre[i] = &rec{id: int64(i), label: "pre", blob: []byte{byte(i)}}
	}
	recSink = append(recSink, pre...)
	failPipe := NewPipeline[rec]()
	for i := range pre {
		it := pre[i]
		idx := i
		failPipe.Submit(fmt.Sprintf("f-%d", idx), func() (*rec, error) {
			if idx == 5 {
				return nil, errBoom
			}
			it.id += 100
			return it, nil
		})
	}
	done, ferr := failPipe.Flush()
	ck(errors.Is(ferr, errBoom), "pipeline error not propagated/wrapped")
	ck(len(done) == 5, fmt.Sprintf("pipeline fail-fast results got=%d want=5", len(done)))
	for i := 0; i < 5; i++ {
		ck(pre[i].id == int64(i+100), "successful pre-error command not applied")
	}
	for i := 5; i < 8; i++ {
		ck(pre[i].id == int64(i), "post-error command was executed despite fail-fast")
	}

	// 通道级联：三级 stage，保序处理，中途 GC。
	gcStress()
	in := make(chan *rec, n)
	for _, it := range items {
		in <- it
	}
	close(in)
	stride := func(k int64, suffix string) func(*rec) *rec {
		return func(r *rec) *rec {
			r.id += k
			r.label += suffix
			return r
		}
	}
	s1 := stage(in, stride(1, "-a"))
	s2 := stage(s1, stride(2, "-b"))
	s3 := stage(s2, stride(3, "-c"))

	got := 0
	for r := range s3 {
		ck(got < n, "stage pipeline emitted too many records")
		if got < n {
			ck(r == items[got], fmt.Sprintf("stage pipeline order/pointer mismatch at %d", got))
			ck(r.label == fmt.Sprintf("i%d-g-a-b-c", got), "stage pipeline label corrupted")
			ck(r.id == int64(got+got%3+1+1+2+3), "stage pipeline id corrupted")
		}
		got++
		if got%64 == 0 {
			gcStress()
		}
	}
	ck(got == n, fmt.Sprintf("stage pipeline count got=%d want=%d", got, n))

	// 第二次实例化 Pipeline[int64]：值类型结果。
	ip := NewPipeline[int64]()
	for i := 0; i < 32; i++ {
		v := int64(i)
		ip.Submit("mul", func() (*int64, error) {
			r := v * 3
			return &r, nil
		})
	}
	gcStress()
	iout, err := ip.Flush()
	ck(err == nil && len(iout) == 32, "int64 pipeline flush failed")
	for i := range iout {
		ck(*iout[i] == int64(i*3), "int64 pipeline result corrupted after GC")
	}

	finish("l01_generic_pipeline")
}
