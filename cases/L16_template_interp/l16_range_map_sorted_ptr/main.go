// L16: text/template 对 map 的 range 按键排序，混合指针切片/nil 元素/缺 key；跨 GC 输出一致。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"text/template"
)

type node struct {
	Name string
	N    int
	Next *node
}

func (n *node) Label() string {
	if n == nil {
		return "nil"
	}
	return n.Name + "#" + strconv.Itoa(n.N)
}

type data struct {
	ByName  map[string]*node
	ByInt   map[int][]*node
	Ptrs    []*node
	Nested  map[string]map[int]*node
	Missing map[string]*node
	Empty   map[string]*node

	// 仅用于身份校验，不参与模板取值。
	a, b, x, y, z, l1, l2, a3, z5 *node
}

const tplSrc = `A:{{range $k, $v := .ByName}}{{$k}}={{if $v}}{{$v.Label}}{{else}}nil{{end}},{{end}}|` +
	`B:{{range $k, $vs := .ByInt}}{{$k}}:[{{range $j, $v := $vs}}{{$j}}{{$v.Label}},{{end}}];{{end}}|` +
	`C:{{range $i, $p := .Ptrs}}{{$i}}={{if $p}}{{$p.Label}}{{with $p.Next}}>{{.Label}}{{end}}{{else}}nil{{end}},{{end}}|` +
	`D:{{.Missing.k}}|E:{{if .Missing.k}}Y{{else}}N{{end}}|F:{{with .Missing.k}}Y{{else}}N{{end}}|` +
	`G:{{range .Empty}}x{{else}}empty{{end}}|` +
	`H:{{range $k, $m := .Nested}}{{$k}}:{{range $ik, $v := $m}}{{$ik}}={{if $v}}{{$v.Label}}{{else}}nil{{end}};{{end}}{{end}}|` +
	`I:{{range .ByName}}{{if .}}{{.Label}}{{else}}nil{{end}}{{end}}`

const want = "A:a=a#1,b=b#2,c=nil,|B:2:[0z#26,];10:[0x#10,1y#20,];|" +
	"C:0=l1#1,1=nil,2=l2#2>l3#3,|D:<no value>|E:N|F:N|G:empty|" +
	"H:a:3=a3#3;z:1=nil;5=z5#5;|I:a#1b#2nil"

var (
	failed  int32
	gcSink  [][]byte
	live    []any
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
	trash := make([]*node, 128)
	for i := range trash {
		trash[i] = &node{Name: "garbage", N: i, Next: &node{Name: "n", N: i}}
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
	_ = trash
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
	if n > 200000 {
		n = 200000
	}
	return n
}

func newData(rng *rand.Rand) data {
	a := &node{Name: "a", N: 1, Next: &node{Name: "n", N: 9}}
	b := &node{Name: "b", N: 2}
	x := &node{Name: "x", N: 10}
	y := &node{Name: "y", N: 20}
	z := &node{Name: "z", N: 26}
	l1 := &node{Name: "l1", N: 1}
	l2 := &node{Name: "l2", N: 2, Next: &node{Name: "l3", N: 3}}
	a3 := &node{Name: "a3", N: 3}
	z5 := &node{Name: "z5", N: 5}

	byName := make(map[string]*node, 3)
	for _, i := range rng.Perm(3) {
		switch i {
		case 0:
			byName["a"] = a
		case 1:
			byName["b"] = b
		case 2:
			byName["c"] = nil
		}
	}

	byInt := make(map[int][]*node, 2)
	for _, i := range rng.Perm(2) {
		if i == 0 {
			byInt[10] = []*node{x, y}
		} else {
			byInt[2] = []*node{z}
		}
	}

	nested := make(map[string]map[int]*node, 2)
	for _, i := range rng.Perm(2) {
		if i == 0 {
			m := make(map[int]*node, 2)
			for _, j := range rng.Perm(2) {
				if j == 0 {
					m[5] = z5
				} else {
					m[1] = nil
				}
			}
			nested["z"] = m
		} else {
			nested["a"] = map[int]*node{3: a3}
		}
	}

	return data{
		ByName:  byName,
		ByInt:   byInt,
		Ptrs:    []*node{l1, nil, l2},
		Nested:  nested,
		Missing: map[string]*node{},
		Empty:   nil,
		a:       a, b: b, x: x, y: y, z: z, l1: l1, l2: l2, a3: a3, z5: z5,
	}
}

func execute(t *template.Template, d data) (string, error) {
	var sb strings.Builder
	err := t.Execute(&sb, d)
	return sb.String(), err
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(12)
	rng := rand.New(rand.NewSource(uafSeed))

	t := template.Must(template.New("ranges").Parse(tplSrc))
	live = append(live, t)

	for r := 0; r < rounds; r++ {
		d := newData(rng)
		live = append(live, d.a, d.b, d.x, d.y, d.z, d.l1, d.l2, d.a3, d.z5, d.Ptrs, d.ByInt, d.Nested)

		out, err := execute(t, d)
		ck(err == nil, fmt.Sprintf("round %d execute: %v", r, err))
		ck(out == want, fmt.Sprintf("round %d output\n got: %q\nwant: %q", r, out, want))
		ck(d.ByName["a"] == d.a && d.ByName["b"] == d.b,
			fmt.Sprintf("round %d map value identity wrong", r))
		ck(d.ByName["c"] == nil, fmt.Sprintf("round %d nil map value lost", r))
		ck(len(d.ByInt[2]) == 1 && d.ByInt[2][0] == d.z,
			fmt.Sprintf("round %d ByInt[2] identity wrong", r))
		ck(len(d.ByInt[10]) == 2 && d.ByInt[10][1] == d.y,
			fmt.Sprintf("round %d ByInt[10] identity wrong", r))
		ck(d.Ptrs[2].Next != nil && d.Ptrs[2].Next.N == 3,
			fmt.Sprintf("round %d pointer slice next chain wrong", r))
		ck(d.Nested["a"][3] == d.a3 && d.Nested["z"][5] == d.z5,
			fmt.Sprintf("round %d nested map identity wrong", r))

		gcStress()

		ck(d.ByName["a"] != nil && d.ByName["a"].Name == "a" && d.ByName["a"].N == 1,
			fmt.Sprintf("round %d map entry after GC", r))
		ck(d.ByName["c"] == nil, fmt.Sprintf("round %d nil map entry after GC", r))
		ck(d.ByInt[10][1] == d.y && d.ByInt[10][1].N == 20,
			fmt.Sprintf("round %d map slice pointer after GC", r))
		ck(d.Ptrs[0] == d.l1 && d.Ptrs[1] == nil && d.Ptrs[2].Next.N == 3,
			fmt.Sprintf("round %d pointer slice after GC", r))
		ck(d.Nested["z"][1] == nil && d.Nested["z"][5] == d.z5,
			fmt.Sprintf("round %d nested map after GC", r))

		out2, err := execute(t, d)
		ck(err == nil, fmt.Sprintf("round %d execute after GC: %v", r, err))
		ck(out2 == want, fmt.Sprintf("round %d output after GC\n got: %q\nwant: %q", r, out2, want))
	}

	finish("l16_range_map_sorted_ptr")
}
