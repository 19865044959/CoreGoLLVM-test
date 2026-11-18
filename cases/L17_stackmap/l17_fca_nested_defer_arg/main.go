// L17: 嵌套 FCA 聚合作为 defer 实参。grid（数组套 struct 套 struct/数组/any）
// 按值返回后复制进 defer 记录；注册后清零局部副本、再按值返回另一个 grid。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	name string
}

type leaf struct {
	p *item
	s string
	n int64
}

type row struct {
	a    leaf
	b    leaf
	arr  [2]leaf
	anyv any
}

type subAgg struct {
	rows [2]row
	list []*item
	tag  string
}

type grid struct {
	rows  [2]row
	sub   subAgg
	ptrs  [3]*item
	label string
	anyv  any
}

type boxed struct {
	v    int64
	name string
}

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func envRounds(def, max int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > max {
				return max
			}
			return v
		}
	}
	return def
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func mkLeaf(base int64, tag string) leaf {
	return leaf{p: &item{v: base, name: tag}, s: tag, n: base % 97}
}

//go:noinline
func makeGrid(base int64, ptrAny bool) grid {
	anyv := any(boxed{v: base + 40, name: "any"})
	if ptrAny {
		anyv = &item{v: base + 40, name: "any"}
	}
	return grid{
		rows: [2]row{
			{
				a:    mkLeaf(base+1, "a"),
				b:    mkLeaf(base+2, "b"),
				arr:  [2]leaf{mkLeaf(base+3, "c"), mkLeaf(base+4, "d")},
				anyv: boxed{v: base + 5, name: "r0"},
			},
			{
				a:    mkLeaf(base+6, "e"),
				b:    mkLeaf(base+7, "f"),
				arr:  [2]leaf{mkLeaf(base+8, "g"), mkLeaf(base+9, "h")},
				anyv: &item{v: base + 10, name: "r1"},
			},
		},
		sub: subAgg{
			rows: [2]row{
				{a: mkLeaf(base+11, "i"), b: mkLeaf(base+12, "j"), arr: [2]leaf{mkLeaf(base+13, "k"), mkLeaf(base+14, "l")}},
				{a: mkLeaf(base+15, "m"), b: mkLeaf(base+16, "n"), arr: [2]leaf{mkLeaf(base+17, "o"), mkLeaf(base+18, "p")}},
			},
			list: []*item{{v: base + 19, name: "sub0"}, {v: base + 20, name: "sub1"}},
			tag:  "sub",
		},
		ptrs:  [3]*item{{v: base + 21, name: "p0"}, {v: base + 22, name: "p1"}, {v: base + 23, name: "p2"}},
		label: "grid",
		anyv:  anyv,
	}
}

func gridSum(base int64) int64 {
	sum := int64(0)
	// rows 0/1：a,b,arr[0],arr[1],anyv
	sum += (base + 1) + (base + 2) + (base + 3) + (base + 4) + (base + 5)
	sum += (base + 6) + (base + 7) + (base + 8) + (base + 9) + (base + 10)
	// sub.rows：每行 a,b,arr[0],arr[1]
	sum += (base + 11) + (base + 12) + (base + 13) + (base + 14)
	sum += (base + 15) + (base + 16) + (base + 17) + (base + 18)
	sum += (base + 19) + (base + 20)
	sum += (base + 21) + (base + 22) + (base + 23)
	sum += base + 40
	return sum
}

func verifyLeaf(l leaf, base int64, where string) int64 {
	if l.p == nil || l.p.v != base || l.s == "" || l.s != l.p.name || l.n != base%97 {
		ck(false, fmt.Sprintf("%s: leaf base=%d corrupted seed=%d", where, base, uafSeed))
		return 0
	}
	return l.p.v
}

func verifyRow(r row, base int64, where string, wantBoxedAny bool, anyV int64, anyName string) int64 {
	sum := verifyLeaf(r.a, base+1, where)
	sum += verifyLeaf(r.b, base+2, where)
	sum += verifyLeaf(r.arr[0], base+3, where)
	sum += verifyLeaf(r.arr[1], base+4, where)
	if wantBoxedAny {
		x, ok := r.anyv.(boxed)
		if !ok || x.v != anyV || x.name != anyName {
			ck(false, fmt.Sprintf("%s: row anyv(boxed) corrupted seed=%d", where, uafSeed))
		} else {
			sum += x.v
		}
	} else {
		x, ok := r.anyv.(*item)
		if !ok || x == nil || x.v != anyV || x.name != anyName {
			ck(false, fmt.Sprintf("%s: row anyv(*item) corrupted seed=%d", where, uafSeed))
		} else {
			sum += x.v
		}
	}
	return sum
}

func verifySub(s subAgg, base int64, where string) int64 {
	sum := int64(0)
	for i, r := range s.rows {
		rb := base + 11 + int64(4*i)
		sum += verifyLeaf(r.a, rb, where)
		sum += verifyLeaf(r.b, rb+1, where)
		sum += verifyLeaf(r.arr[0], rb+2, where)
		sum += verifyLeaf(r.arr[1], rb+3, where)
	}
	if len(s.list) != 2 || s.list[0] == nil || s.list[1] == nil {
		ck(false, fmt.Sprintf("%s: sub.list corrupted seed=%d", where, uafSeed))
	} else if s.list[0].v != base+19 || s.list[1].v != base+20 {
		ck(false, fmt.Sprintf("%s: sub.list values corrupted seed=%d", where, uafSeed))
	} else {
		sum += s.list[0].v + s.list[1].v
	}
	if s.tag != "sub" {
		ck(false, fmt.Sprintf("%s: sub.tag=%q seed=%d", where, s.tag, uafSeed))
	}
	return sum
}

func verifyGrid(g grid, base int64, ptrAny bool, where string) int64 {
	sum := verifyRow(g.rows[0], base, where, true, base+5, "r0")
	sum += verifyRow(g.rows[1], base+5, where, false, base+10, "r1")
	sum += verifySub(g.sub, base, where)
	for i := 0; i < 3; i++ {
		if g.ptrs[i] == nil || g.ptrs[i].v != base+21+int64(i) {
			ck(false, fmt.Sprintf("%s: ptrs[%d] corrupted seed=%d", where, i, uafSeed))
			continue
		}
		sum += g.ptrs[i].v
	}
	if g.label != "grid" {
		ck(false, fmt.Sprintf("%s: label=%q seed=%d", where, g.label, uafSeed))
	}
	if ptrAny {
		x, ok := g.anyv.(*item)
		if !ok || x == nil || x.v != base+40 || x.name != "any" {
			ck(false, fmt.Sprintf("%s: grid anyv(*item) corrupted seed=%d", where, uafSeed))
		} else {
			sum += x.v
		}
	} else {
		x, ok := g.anyv.(boxed)
		if !ok || x.v != base+40 || x.name != "any" {
			ck(false, fmt.Sprintf("%s: grid anyv(boxed) corrupted seed=%d", where, uafSeed))
		} else {
			sum += x.v
		}
	}
	return sum
}

//go:noinline
func takeGrid(g grid, base int64, ptrAny bool, sum *int64, where string) {
	gcStress()
	s1 := verifyGrid(g, base, ptrAny, where+"-before")
	gcStress()
	s2 := verifyGrid(g, base, ptrAny, where+"-after")
	ck(s1 == s2, fmt.Sprintf("%s: FCA defer record changed across GC seed=%d", where, uafSeed))
	*sum += s2
}

//go:noinline
func run(base int64, ptrAny bool, where string) (out grid, sum int64) {
	g := makeGrid(base, ptrAny)
	if got := verifyGrid(g, base, ptrAny, where+"-before"); got != gridSum(base) {
		ck(false, fmt.Sprintf("%s: returned grid checksum=%d want=%d seed=%d", where, got, gridSum(base), uafSeed))
	}
	// grid 整体复制进 defer 记录；随后清空局部，只有 defer 记录持有副本。
	defer takeGrid(g, base, ptrAny, &sum, where)
	g = grid{}
	gcStress()
	if g.ptrs[0] != nil || g.sub.rows[0].a.p != nil {
		ck(false, fmt.Sprintf("%s: local grid not cleared seed=%d", where, uafSeed))
	}
	out = makeGrid(base+1, ptrAny)
	gcStress()
	return
}

//go:noinline
func mutateByVal(g grid, delta int64) grid {
	// 只替换指针字段，不改被指向对象，验证 caller 的 grid 副本不会被带动。
	g.rows[0].a.p = &item{v: 9000 + delta, name: "new-a"}
	g.rows[1].arr[1].p = &item{v: 9100 + delta, name: "new-d"}
	g.ptrs[2] = &item{v: 9200 + delta, name: "new-p"}
	g.anyv = boxed{v: 9300 + delta, name: "new-any"}
	gcStress()
	return g
}

func main() {
	uafSeed = seedFromEnv()
	rounds := envRounds(32, 1024)

	for r := 0; r < rounds; r++ {
		base := int64(1000*r) + uafSeed%19
		ptrAny := (r+int(uafSeed))%2 == 0
		where := fmt.Sprintf("round %d", r)

		out, sum := run(base, ptrAny, where)
		gcStress()
		if got := verifyGrid(out, base+1, ptrAny, where+"-out"); got != gridSum(base+1) {
			ck(false, fmt.Sprintf("%s: named return grid checksum=%d want=%d seed=%d", where, got, gridSum(base+1), uafSeed))
		}
		if sum != gridSum(base) {
			ck(false, fmt.Sprintf("%s: deferred FCA sum=%d want=%d seed=%d", where, sum, gridSum(base), uafSeed))
		}

		g := makeGrid(base+100, ptrAny)
		mut := mutateByVal(g, int64(r)+uafSeed%11)
		gcStress()
		if got := verifyGrid(g, base+100, ptrAny, where+"-orig"); got != gridSum(base+100) {
			ck(false, fmt.Sprintf("%s: by-value callee mutated caller grid seed=%d", where, uafSeed))
		}
		if mut.rows[0].a.p == nil || mut.rows[0].a.p.v != 9000+int64(r)+uafSeed%11 {
			ck(false, fmt.Sprintf("%s: by-value return row mutation lost seed=%d", where, uafSeed))
		}
		if g.rows[0].a.p == nil || g.rows[0].a.p.v != base+101 {
			ck(false, fmt.Sprintf("%s: caller grid row pointer changed seed=%d", where, uafSeed))
		}

		var directSum int64
		direct := makeGrid(base+200, ptrAny)
		takeGrid(direct, base+200, ptrAny, &directSum, where+"-direct")
		gcStress()
		if directSum != gridSum(base+200) {
			ck(false, fmt.Sprintf("%s: direct FCA arg sum=%d want=%d seed=%d", where, directSum, gridSum(base+200), uafSeed))
		}
	}

	finish("l17_fca_nested_defer_arg")
}
