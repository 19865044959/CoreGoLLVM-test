// L06: 接口解释器：指令为 interface、栈/环境为 any 风格容器，覆盖接口派发、嵌入指令、
// 宿主回调、错误路径与逐指令 GC 后重算。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
}

type vm struct {
	stack   []int64
	env     map[string]int64
	tags    map[string]*node
	pc      int
	steps   int
	progLen int
}

func (m *vm) pop() (int64, error) {
	if len(m.stack) == 0 {
		return 0, fmt.Errorf("stack underflow")
	}
	v := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	return v, nil
}

type instruction interface {
	exec(*vm) error
	opName() string
}

type push struct{ v int64 }

func (p push) exec(m *vm) error { m.stack = append(m.stack, p.v); m.pc++; return nil }
func (push) opName() string     { return "push" }

type pushVar struct{ name string }

func (p pushVar) exec(m *vm) error {
	v, ok := m.env[p.name]
	if !ok {
		return fmt.Errorf("undefined var %q", p.name)
	}
	m.stack = append(m.stack, v)
	m.pc++
	return nil
}
func (pushVar) opName() string { return "pushVar" }

type binOp struct{ op string }

func (b binOp) exec(m *vm) error {
	if len(m.stack) < 2 {
		return fmt.Errorf("stack underflow: %s", b.op)
	}
	y, _ := m.pop()
	x, _ := m.pop()
	var r int64
	switch b.op {
	case "add":
		r = x + y
	case "sub":
		r = x - y
	case "mul":
		r = x * y
	case "div":
		if y == 0 {
			return fmt.Errorf("division by zero")
		}
		r = x / y
	default:
		return fmt.Errorf("unknown binop %q", b.op)
	}
	m.stack = append(m.stack, r)
	m.pc++
	return nil
}
func (b binOp) opName() string { return b.op }

type neg struct{}

func (neg) exec(m *vm) error {
	v, err := m.pop()
	if err != nil {
		return err
	}
	m.stack = append(m.stack, -v)
	m.pc++
	return nil
}
func (neg) opName() string { return "neg" }

type store struct{ name string }

func (s store) exec(m *vm) error {
	v, err := m.pop()
	if err != nil {
		return err
	}
	m.env[s.name] = v
	m.pc++
	return nil
}
func (store) opName() string { return "store" }

type load struct{ name string }

func (l load) exec(m *vm) error {
	v, ok := m.env[l.name]
	if !ok {
		return fmt.Errorf("undefined var %q", l.name)
	}
	m.stack = append(m.stack, v)
	m.pc++
	return nil
}
func (load) opName() string { return "load" }

type jump struct{ target int }

func (j jump) exec(m *vm) error { m.pc = j.target; return nil }
func (jump) opName() string     { return "jump" }

type jumpIfZero struct{ target int }

func (j jumpIfZero) exec(m *vm) error {
	v, err := m.pop()
	if err != nil {
		return err
	}
	if v == 0 {
		m.pc = j.target
	} else {
		m.pc++
	}
	return nil
}
func (jumpIfZero) opName() string { return "jz" }

type badInstr struct{ msg string }

func (b badInstr) exec(*vm) error { return fmt.Errorf("bad instruction: %s", b.msg) }
func (badInstr) opName() string   { return "bad" }

type hostCall struct{ name string }

func (h hostCall) exec(m *vm) error {
	f, ok := builtins[h.name]
	if !ok {
		return fmt.Errorf("unknown host %q", h.name)
	}
	if err := f(m); err != nil {
		return err
	}
	m.pc++
	return nil
}
func (h hostCall) opName() string { return "host:" + h.name }

// traced 用嵌入接口再包一层，验证指令流的接口提升派发。
type traced struct{ instruction }

var builtins = map[string]func(*vm) error{
	"double": func(m *vm) error {
		v, err := m.pop()
		if err != nil {
			return err
		}
		m.stack = append(m.stack, v*2)
		return nil
	},
	"addTag": func(m *vm) error {
		v, err := m.pop()
		if err != nil {
			return err
		}
		n, ok := m.tags["n"]
		if !ok || n == nil {
			return fmt.Errorf("missing tag")
		}
		m.stack = append(m.stack, v+n.V)
		return nil
	},
	"dup": func(m *vm) error {
		if len(m.stack) == 0 {
			return fmt.Errorf("stack underflow: dup")
		}
		m.stack = append(m.stack, m.stack[len(m.stack)-1])
		return nil
	},
}

var (
	failed  int32
	gcSink  [][]byte
	anySink any
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

func envInt(name string, def, min, max int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func run(m *vm, prog []instruction, gcEvery int) error {
	m.pc = 0
	m.steps = 0
	m.progLen = len(prog)
	for m.pc < len(prog) {
		if m.pc < 0 {
			return fmt.Errorf("pc out of range: %d", m.pc)
		}
		ins := prog[m.pc]
		if err := ins.exec(m); err != nil {
			return fmt.Errorf("pc %d %s: %w", m.pc, ins.opName(), err)
		}
		m.steps++
		if m.pc > len(prog) {
			return fmt.Errorf("pc out of range: %d", m.pc)
		}
		if gcEvery > 0 && m.steps%gcEvery == 0 {
			gcStress()
		}
	}
	if len(m.stack) != 1 {
		return fmt.Errorf("stack depth %d at exit", len(m.stack))
	}
	return nil
}

func newVM(tagV int64) *vm {
	return &vm{
		env:  map[string]int64{},
		tags: map[string]*node{"n": {V: tagV, Label: "vm-tag"}},
	}
}

func arithmeticProgram(a, b int64) []instruction {
	return []instruction{
		push{a}, push{3}, binOp{"add"}, push{2}, binOp{"mul"},
		push{b}, binOp{"sub"}, store{"r"}, load{"r"},
	}
}

func conditionalProgram(x int64) []instruction {
	return []instruction{
		push{x}, jumpIfZero{target: 4}, push{10}, jump{target: 5},
		push{20}, store{"r"}, load{"r"},
	}
}

func hostProgram(a int64) []instruction {
	return []instruction{
		push{a}, hostCall{"double"}, hostCall{"addTag"},
	}
}

func squareProgram(a int64) []instruction {
	return []instruction{push{a}, hostCall{"dup"}, binOp{"mul"}}
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 40, 1, 2000)
	width := envInt("TORTURE_WIDTH", 64, 1, 20000)

	a := uafSeed%17 + 1
	b := uafSeed%13 + 2
	x := uafSeed % 2
	tagV := uafSeed%7 + 3

	m := newVM(tagV)
	prog := arithmeticProgram(a, b)
	if err := run(m, prog, 2); err != nil {
		ck(false, "arithmetic program runs: "+err.Error())
	} else {
		ck(m.stack[0] == (a+3)*2-b, "arithmetic result")
		ck(m.env["r"] == (a+3)*2-b, "store/load env result")
	}
	arithVM := m

	// 指令动态类型分类（type switch）。
	counts := map[string]int{}
	for _, ins := range prog {
		switch v := ins.(type) {
		case push:
			counts["push"]++
		case store:
			counts["store"]++
		case load:
			counts["load"]++
		case binOp:
			counts[v.op]++
		default:
			counts["other"]++
		}
	}
	ck(counts["push"] == 4 && counts["store"] == 1 && counts["load"] == 1 && counts["add"] == 1 &&
		counts["mul"] == 1 && counts["sub"] == 1 && counts["other"] == 0, "instruction type switch counts")

	// 条件跳转两条路径。
	m = newVM(tagV)
	if err := run(m, conditionalProgram(x), 1); err != nil {
		ck(false, "conditional program runs: "+err.Error())
	} else {
		want := int64(10)
		if x == 0 {
			want = 20
		}
		ck(m.stack[0] == want, "conditional jump result")
	}

	// 宿主回调与嵌入接口指令。
	m = newVM(tagV)
	hostProg := hostProgram(a)
	if err := run(m, hostProg, 1); err != nil {
		ck(false, "host program runs: "+err.Error())
	} else {
		ck(m.stack[0] == 2*a+tagV, "host callback result")
	}
	m = newVM(tagV)
	if err := run(m, squareProgram(a), 1); err != nil {
		ck(false, "square program runs: "+err.Error())
	} else {
		ck(m.stack[0] == a*a, "dup host result")
	}
	m = newVM(tagV)
	wrapped := []instruction{traced{instruction: push{1}}, traced{instruction: push{2}}, traced{instruction: binOp{"add"}}}
	if err := run(m, wrapped, 1); err != nil {
		ck(false, "traced program runs: "+err.Error())
	} else {
		ck(m.stack[0] == 3, "embedded interface instruction dispatch")
	}

	// 错误路径：错误文本确定。
	errCases := []struct {
		name string
		prog []instruction
		want string
	}{
		{"underflow", []instruction{binOp{"add"}}, "stack underflow"},
		{"divide", []instruction{push{1}, push{0}, binOp{"div"}}, "division by zero"},
		{"jump", []instruction{jump{99}}, "pc out of range"},
		{"host", []instruction{push{1}, hostCall{"nope"}}, "unknown host"},
		{"bad", []instruction{badInstr{"boom"}}, "bad instruction: boom"},
		{"var", []instruction{pushVar{"missing"}}, "undefined var"},
		{"empty", nil, "stack depth 0 at exit"},
	}
	for _, tc := range errCases {
		m = newVM(tagV)
		err := run(m, tc.prog, 0)
		ck(err != nil && strings.Contains(err.Error(), tc.want), "error path: "+tc.name)
	}

	// 程序、环境、tag 指针只由接口容器保活，GC 后逐指令重算。
	anySink = []any{prog, arithVM, countSnapshot(counts)}
	gcStress()
	prog2 := anySink.([]any)[0].([]instruction)
	savedVM := anySink.([]any)[1].(*vm)
	ck(savedVM.tags["n"].Label == "vm-tag", "saved vm tag pointer after GC")
	ck(savedVM.env["r"] == (a+3)*2-b, "saved vm env after GC")
	m = newVM(tagV)
	if err := run(m, prog2, 1); err != nil {
		ck(false, "program after GC: "+err.Error())
	} else {
		ck(m.stack[0] == (a+3)*2-b, "program after GC result")
	}
	ck(reflect.TypeOf(prog2[0]) == reflect.TypeOf(push{}), "instruction dynamic type after GC")

	// TORTURE：重复解释 + 长程序放大器。
	longProg := make([]instruction, 0, 2*width+1)
	longProg = append(longProg, push{1})
	for i := 0; i < width; i++ {
		longProg = append(longProg, push{1}, binOp{"add"})
	}
	for r := 0; r < rounds; r++ {
		m = newVM(tagV + int64(r))
		if err := run(m, longProg, 0); err != nil {
			ck(false, "long program runs: "+err.Error())
			continue
		}
		ck(m.stack[0] == int64(width+1), "long program result")
		ck(m.steps == 1+2*width, "long program step count")
		if r%8 == 0 {
			gcStress()
			ck(m.tags["n"].V == tagV+int64(r), "long program tag after GC")
		}
	}

	finish("l06_iface_vm_stack_dispatch")
}

func countSnapshot(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
