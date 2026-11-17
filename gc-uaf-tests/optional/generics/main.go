// 可选用例（默认不跑）：泛型需要前端支持 Go 1.18+ 语法。
// 若内网 CoreGoLLVM 前端支持泛型，把它拷进 tests/ 或手工编译运行。
package main

import (
	"fmt"
	"runtime"
)

type node struct{ v int64 }

type box[T any] struct{ p *T }

//go:noinline
func makeBox[T any](v T) box[T] { return box[T]{p: &v} }

//go:noinline
func gcHere() { runtime.GC() }

func id[T any](v T) T { return v }

func main() {
	b := makeBox(node{v: 42})
	gcHere()
	if b.p.v != 42 {
		panic("generic box corrupted")
	}
	m := map[string]box[node]{}
	m["a"] = b
	gcHere()
	if m["a"].p.v != 42 {
		panic("generic map corrupted")
	}
	sl := []box[node]{b, b}
	gcHere()
	if sl[1].p.v != 42 {
		panic("generic slice corrupted")
	}
	if id(node{v: 7}).v != 7 {
		panic("generic id")
	}
	fmt.Println("ALL PASS optional_generics")
}
