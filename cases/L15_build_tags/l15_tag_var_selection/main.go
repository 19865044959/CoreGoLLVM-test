// L15: tag 选择方法。同一份代码用三种机制选择实现：
//   1) 包级函数变量 NewNode 在 tag 文件中指向不同构造器；
//   2) 两个 builder 在 init 注册到 registry，按优先级挑最高；
//   3) 泛型函数以 tag 选择的类型/方法集实例化。
// 默认构建选中 base；-tags=tagSel + EXPECT_SEL=alt 为手工验证路径。
// TORTURE_ROUNDS 轮内反复经三种机制取对象，验证选择结果稳定且在 GC 后仍有效。
package main

import (
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

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

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func main() {
	variant := os.Getenv("EXPECT_SEL")
	if variant == "" {
		variant = "base"
	}
	ck(variant == "base" || variant == "alt", "EXPECT_SEL must be base or alt")
	if variant != "alt" {
		variant = "base"
	}
	scale := int64(2)
	prio := 10
	if variant == "alt" {
		scale = 5
		prio = 20
	}

	p := &implNode{id: 61, tag: "payload"}

	// 机制 1：tag 文件里的常量与函数变量。
	ck(selVariant == variant, "tag selects variant constant")
	n := NewNode(7)
	ck(n.id == 7 && n.tag == variant, "package-level function variable selected by tag")
	ck(n.Kind() == "kind:"+variant, "method from tag-selected file")
	ck(n.Scale() == scale, "scale method from tag-selected file")

	// 机制 2：init 注册表按优先级选择。
	names := make([]string, 0, len(registry))
	for _, e := range registry {
		names = append(names, e.name)
	}
	sort.Strings(names)
	ck(len(names) == 2 && names[0] == variant && names[1] == "plain", "registry contains plain + selected variant")
	chosen := pickHighest()
	ck(chosen.name == variant && chosen.prio == prio, "highest-priority builder is tag-selected variant")
	m := chosen.fn(3)
	ck(m.id == 3 && m.tag == variant && m.Scale() == scale, "registry-selected builder payload")

	inits := append([]string(nil), builderInits...)
	sort.Strings(inits)
	ck(len(inits) == 2 && inits[0] == variant && inits[1] == "plain", "exactly one variant init registered")

	// 机制 3：泛型函数在 tag 选择的类型上实例化。
	ck(buildWith(n) == "G:kind:"+variant, "generic dispatch on tag-selected type")
	ck(buildWith(chosen.fn(4)) == "G:kind:"+variant, "generic dispatch on registry builder result")
	ck(buildWith(constNode{}) == "G:const", "generic dispatch on common type")

	// 压测：三种机制在 TORTURE_ROUNDS 轮内保持一致。
	rounds := envInt("TORTURE_ROUNDS", 3000)
	seed := envInt("UAF_SEED", 1)
	extra := int64(seed % 3)
	keep := NewNode(99)
	var sum int64
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}
	for r := 0; r < rounds; r++ {
		a := NewNode(int64(r))
		b := chosen.fn(int64(r))
		sum += a.Scale()*int64(r) + b.Scale() + extra
		if r%64 == 0 {
			buf := make([]byte, 2048)
			buf[0] = byte(r)
			gcSink = append(gcSink[:0], buf)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}
	wantSum := scale*int64(rounds)*int64(rounds-1)/2 + scale*int64(rounds) + int64(rounds)*extra
	ck(sum == wantSum, fmt.Sprintf("selection checksum mismatch seed=%d rounds=%d", seed, rounds))

	runtime.GC()
	runtime.GC()
	ck(keep.id == 99 && keep.tag == variant && keep.Scale() == scale, "retained node after GC")
	ck(pickHighest().name == variant, "priority selection stable after GC")
	ck(buildWith(NewNode(5)) == "G:kind:"+variant, "generic selection stable after GC")
	ck(p.id == 61 && p.tag == "payload" && p.Scale() == scale, "payload after GC")
	finish("l15_tag_var_selection")
}
