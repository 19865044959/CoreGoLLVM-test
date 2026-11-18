// L15: go:build 表达式组合。覆盖嵌套括号 + AND/OR/NOT、恒真式、矛盾式取反
// 与自定义 tag 组合；默认构建（无自定义 tag）下所有函数的期望值确定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
)

type exprNode struct {
	id   int64
	name string
}

var (
	failed int32
	gcSink [][]byte
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	wantExpr := envOr("EXPECT_EXPR", "off")
	wantDM := envOr("EXPECT_DM", "not-both")
	wantNest := envOr("EXPECT_NEST", "simple")

	p := &exprNode{id: 51, name: "build-expr"}

	ck(exprImpl() == wantExpr, "nested OR/NOT/AND expression selects implementation")
	ck(tautImpl() == "taut", "tautological parenthesized expression compiles")
	ck(impossibleImpl() == "possible", "negated contradiction expression compiles")
	ck(deMorganImpl() == wantDM, "De Morgan style !(A && B) selection")
	ck(nestImpl() == wantNest, "deeply nested parenthesized expression selection")

	gcStress()
	ck(exprImpl() == wantExpr && nestImpl() == wantNest && p.id == 51 && p.name == "build-expr", "expression results after GC")
	finish("l15_build_expr_nested")
}
