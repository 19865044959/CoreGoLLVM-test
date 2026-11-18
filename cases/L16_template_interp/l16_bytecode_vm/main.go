// L16: 把 go/ast 表达式/if 编译为栈式字节码并执行；与 Go 参考实现交叉校验，跨 GC。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const src = `package vm

func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func poly(x int) int {
	return x*x*x - 3*x + 7
}

func clamp(v int) int {
	if v < -100 {
		return -100
	}
	if v > 100 {
		return 100
	}
	return v
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func div(a int, b int) int {
	return a / b
}

func rem(a int, b int) int {
	return a % b
}

func nested(a int, b int) int {
	return poly(a) + clamp(b) - abs(a-b)
}

func inf(n int) int {
	return inf(n)
}
`

const badUnknown = `package vm
func f(a int) int { return nope(a) }
`
const badArity = `package vm
func f(a int) int { return f(a, 1) }
`
const badStmt = `package vm
func f(a int) int {
	for {
		return a
	}
}
`
const badLiteral = `package vm
func f(a int) int { return "x" }
`
const badNoReturn = `package vm
func f(a int) int {
	if a > 0 {
		return a
	}
}
`
const badIdent = `package vm
func f(a int) int { return zzz }
`
const badDup = `package vm
func f(a int) int { return a }
func f(a int) int { return a }
`

type opcode int

const (
	opPush opcode = iota
	opLoad
	opAdd
	opSub
	opMul
	opDiv
	opRem
	opNeg
	opLT
	opLE
	opGT
	opGE
	opEQ
	opNE
	opJump
	opJmpIfZero
	opCall
	opRet
)

func (o opcode) String() string {
	switch o {
	case opPush:
		return "push"
	case opLoad:
		return "load"
	case opAdd:
		return "add"
	case opSub:
		return "sub"
	case opMul:
		return "mul"
	case opDiv:
		return "div"
	case opRem:
		return "rem"
	case opNeg:
		return "neg"
	case opLT:
		return "lt"
	case opLE:
		return "le"
	case opGT:
		return "gt"
	case opGE:
		return "ge"
	case opEQ:
		return "eq"
	case opNE:
		return "ne"
	case opJump:
		return "jump"
	case opJmpIfZero:
		return "jz"
	case opCall:
		return "call"
	case opRet:
		return "ret"
	}
	return "?"
}

type instr struct {
	op   opcode
	val  int64
	name string
	argc int
}

func (in instr) String() string {
	switch in.op {
	case opPush, opLoad, opJump, opJmpIfZero:
		return fmt.Sprintf("%s %d", in.op, in.val)
	case opCall:
		return fmt.Sprintf("call %s/%d", in.name, in.argc)
	}
	return in.op.String()
}

type function struct {
	name   string
	params []string
	code   []instr
	line   int
	pos    token.Pos
}

type program struct {
	funcs []*function
	index map[string]int
	file  *ast.File
	fset  *token.FileSet
}

func (p *program) dump() string {
	var sb strings.Builder
	for _, fn := range p.funcs {
		fmt.Fprintf(&sb, "%s(", fn.name)
		for i, prm := range fn.params {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(prm)
		}
		sb.WriteString("):")
		for _, in := range fn.code {
			sb.WriteString(" ")
			sb.WriteString(in.String())
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

type compiler struct {
	p      *program
	fn     *function
	params map[string]int
}

func (c *compiler) emit(in instr) int {
	c.fn.code = append(c.fn.code, in)
	return len(c.fn.code) - 1
}

func (c *compiler) patch(at, target int) {
	// 跳转目标以字节码下标存储，name 字段仅用于 call 的可读反汇编。
	c.fn.code[at].val = int64(target)
}

func (c *compiler) block(b *ast.BlockStmt) error {
	if b == nil {
		return fmt.Errorf("nil block")
	}
	for _, st := range b.List {
		if err := c.stmt(st); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) stmt(st ast.Stmt) error {
	switch s := st.(type) {
	case *ast.ReturnStmt:
		if len(s.Results) != 1 {
			return fmt.Errorf("unsupported return with %d values", len(s.Results))
		}
		if err := c.expr(s.Results[0]); err != nil {
			return err
		}
		c.emit(instr{op: opRet})
		return nil
	case *ast.BlockStmt:
		return c.block(s)
	case *ast.IfStmt:
		if s.Init != nil {
			return fmt.Errorf("if-init is not supported")
		}
		if err := c.expr(s.Cond); err != nil {
			return err
		}
		jf := c.emit(instr{op: opJmpIfZero})
		if err := c.block(s.Body); err != nil {
			return err
		}
		if s.Else == nil {
			c.patch(jf, len(c.fn.code))
			return nil
		}
		jend := c.emit(instr{op: opJump})
		c.patch(jf, len(c.fn.code))
		if err := c.stmt(s.Else); err != nil {
			return err
		}
		c.patch(jend, len(c.fn.code))
		return nil
	}
	return fmt.Errorf("unsupported statement %T", st)
}

func (c *compiler) expr(e ast.Expr) error {
	switch x := e.(type) {
	case *ast.ParenExpr:
		return c.expr(x.X)
	case *ast.BasicLit:
		if x.Kind != token.INT {
			return fmt.Errorf("unsupported literal %s", x.Kind)
		}
		v, err := strconv.ParseInt(x.Value, 0, 64)
		if err != nil {
			return fmt.Errorf("bad literal %q", x.Value)
		}
		c.emit(instr{op: opPush, val: v})
		return nil
	case *ast.Ident:
		slot, ok := c.params[x.Name]
		if !ok {
			return fmt.Errorf("unknown identifier %q", x.Name)
		}
		c.emit(instr{op: opLoad, val: int64(slot)})
		return nil
	case *ast.UnaryExpr:
		if err := c.expr(x.X); err != nil {
			return err
		}
		switch x.Op {
		case token.ADD:
			return nil
		case token.SUB:
			c.emit(instr{op: opNeg})
			return nil
		}
		return fmt.Errorf("unsupported unary operator %s", x.Op)
	case *ast.BinaryExpr:
		if err := c.expr(x.X); err != nil {
			return err
		}
		if err := c.expr(x.Y); err != nil {
			return err
		}
		var op opcode
		switch x.Op {
		case token.ADD:
			op = opAdd
		case token.SUB:
			op = opSub
		case token.MUL:
			op = opMul
		case token.QUO:
			op = opDiv
		case token.REM:
			op = opRem
		case token.LSS:
			op = opLT
		case token.LEQ:
			op = opLE
		case token.GTR:
			op = opGT
		case token.GEQ:
			op = opGE
		case token.EQL:
			op = opEQ
		case token.NEQ:
			op = opNE
		default:
			return fmt.Errorf("unsupported binary operator %s", x.Op)
		}
		c.emit(instr{op: op})
		return nil
	case *ast.CallExpr:
		id, ok := x.Fun.(*ast.Ident)
		if !ok {
			return fmt.Errorf("unsupported callee %T", x.Fun)
		}
		idx, ok := c.p.index[id.Name]
		if !ok {
			return fmt.Errorf("unknown function %q", id.Name)
		}
		want := len(c.p.funcs[idx].params)
		if len(x.Args) != want {
			return fmt.Errorf("call to %s: want %d args, got %d", id.Name, want, len(x.Args))
		}
		for _, a := range x.Args {
			if err := c.expr(a); err != nil {
				return err
			}
		}
		c.emit(instr{op: opCall, val: int64(idx), name: id.Name, argc: len(x.Args)})
		return nil
	}
	return fmt.Errorf("unsupported expression %T", e)
}

func compileProgram(srcText string) (*program, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "vm.go", srcText, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	p := &program{index: make(map[string]int), file: f, fset: fset}
	var decls []*ast.FuncDecl
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok {
			return nil, fmt.Errorf("unsupported declaration %T", d)
		}
		if fd.Recv != nil {
			return nil, fmt.Errorf("methods are not supported")
		}
		name := fd.Name.Name
		if _, dup := p.index[name]; dup {
			return nil, fmt.Errorf("duplicate function %q", name)
		}
		if fd.Type.Params == nil {
			return nil, fmt.Errorf("function %s has no parameter list", name)
		}
		var params []string
		for _, field := range fd.Type.Params.List {
			if len(field.Names) == 0 {
				return nil, fmt.Errorf("function %s has unnamed parameter", name)
			}
			for _, nm := range field.Names {
				params = append(params, nm.Name)
			}
		}
		p.index[name] = len(p.funcs)
		p.funcs = append(p.funcs, &function{name: name, params: params, line: fset.Position(fd.Pos()).Line, pos: fd.Pos()})
		decls = append(decls, fd)
	}
	if len(p.funcs) == 0 {
		return nil, fmt.Errorf("no functions")
	}
	for i, fd := range decls {
		fn := p.funcs[i]
		c := &compiler{p: p, fn: fn, params: make(map[string]int, len(fn.params))}
		for slot, name := range fn.params {
			c.params[name] = slot
		}
		if err := c.block(fd.Body); err != nil {
			return nil, fmt.Errorf("%s: %v", fn.name, err)
		}
		if fd.Body == nil || len(fd.Body.List) == 0 {
			return nil, fmt.Errorf("%s: empty body", fn.name)
		}
		if _, ok := fd.Body.List[len(fd.Body.List)-1].(*ast.ReturnStmt); !ok {
			return nil, fmt.Errorf("%s: missing final return", fn.name)
		}
	}
	return p, nil
}

type machine struct {
	p        *program
	maxSteps int
	maxDepth int
	steps    int
}

func (m *machine) callByName(name string, args []int64) (int64, error) {
	idx, ok := m.p.index[name]
	if !ok {
		return 0, fmt.Errorf("unknown function %q", name)
	}
	m.steps = 0
	return m.call(idx, args, 0)
}

func (m *machine) call(idx int, args []int64, depth int) (int64, error) {
	if depth > m.maxDepth {
		return 0, fmt.Errorf("call depth exceeded (%d) at %s", m.maxDepth, m.p.funcs[idx].name)
	}
	fn := m.p.funcs[idx]
	if len(args) != len(fn.params) {
		return 0, fmt.Errorf("%s: want %d args, got %d", fn.name, len(fn.params), len(args))
	}
	locals := append([]int64(nil), args...)
	stack := make([]int64, 0, 16)
	push := func(v int64) { stack = append(stack, v) }
	pop := func() (int64, error) {
		if len(stack) == 0 {
			return 0, fmt.Errorf("%s: stack underflow", fn.name)
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v, nil
	}
	bin := func(op func(a, b int64) int64) error {
		b, err := pop()
		if err != nil {
			return err
		}
		a, err := pop()
		if err != nil {
			return err
		}
		push(op(a, b))
		return nil
	}
	ip := 0
	for ip < len(fn.code) {
		in := fn.code[ip]
		ip++
		m.steps++
		if m.steps > m.maxSteps {
			return 0, fmt.Errorf("step limit exceeded (%d)", m.maxSteps)
		}
		switch in.op {
		case opPush:
			push(in.val)
		case opLoad:
			if in.val < 0 || int(in.val) >= len(locals) {
				return 0, fmt.Errorf("%s: bad local slot %d", fn.name, in.val)
			}
			push(locals[in.val])
		case opAdd:
			if err := bin(func(a, b int64) int64 { return a + b }); err != nil {
				return 0, err
			}
		case opSub:
			if err := bin(func(a, b int64) int64 { return a - b }); err != nil {
				return 0, err
			}
		case opMul:
			if err := bin(func(a, b int64) int64 { return a * b }); err != nil {
				return 0, err
			}
		case opDiv, opRem:
			b, err := pop()
			if err != nil {
				return 0, err
			}
			a, err := pop()
			if err != nil {
				return 0, err
			}
			if b == 0 {
				return 0, fmt.Errorf("%s: division by zero", fn.name)
			}
			if in.op == opDiv {
				push(a / b)
			} else {
				push(a % b)
			}
		case opNeg:
			v, err := pop()
			if err != nil {
				return 0, err
			}
			push(-v)
		case opLT, opLE, opGT, opGE, opEQ, opNE:
			b, err := pop()
			if err != nil {
				return 0, err
			}
			a, err := pop()
			if err != nil {
				return 0, err
			}
			var r bool
			switch in.op {
			case opLT:
				r = a < b
			case opLE:
				r = a <= b
			case opGT:
				r = a > b
			case opGE:
				r = a >= b
			case opEQ:
				r = a == b
			case opNE:
				r = a != b
			}
			if r {
				push(1)
			} else {
				push(0)
			}
		case opJump:
			ip = int(in.val)
		case opJmpIfZero:
			v, err := pop()
			if err != nil {
				return 0, err
			}
			if v == 0 {
				ip = int(in.val)
			}
		case opCall:
			if in.argc > len(stack) {
				return 0, fmt.Errorf("%s: call stack underflow", fn.name)
			}
			callArgs := make([]int64, in.argc)
			for i := in.argc - 1; i >= 0; i-- {
				v, err := pop()
				if err != nil {
					return 0, err
				}
				callArgs[i] = v
			}
			v, err := m.call(int(in.val), callArgs, depth+1)
			if err != nil {
				return 0, err
			}
			push(v)
		case opRet:
			v, err := pop()
			if err != nil {
				return 0, err
			}
			return v, nil
		default:
			return 0, fmt.Errorf("%s: bad opcode %v", fn.name, in.op)
		}
	}
	return 0, fmt.Errorf("%s: fell off end of bytecode", fn.name)
}

var (
	failed  int32
	gcSink  [][]byte
	livePgm *program
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
	for i := 0; i < 32; i++ {
		_ = make([]instr, 256)
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
	if n > 5000 {
		n = 5000
	}
	return n
}

func refFib(n int64) int64 {
	if n < 2 {
		return n
	}
	return refFib(n-1) + refFib(n-2)
}

func refPoly(x int64) int64 { return x*x*x - 3*x + 7 }

func refClamp(v int64) int64 {
	if v < -100 {
		return -100
	}
	if v > 100 {
		return 100
	}
	return v
}

func refAbs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func refNested(a, b int64) int64 { return refPoly(a) + refClamp(b) - refAbs(a-b) }

func main() {
	uafSeed = envSeed()
	p, err := compileProgram(src)
	ck(err == nil, fmt.Sprintf("compile valid program: %v", err))
	if err != nil {
		finish("l16_bytecode_vm")
		return
	}
	livePgm = p
	ck(len(p.funcs) == 8, fmt.Sprintf("funcs=%d want 8", len(p.funcs)))
	dump := p.dump()
	ck(len(dump) > 0, "empty disassembly")

	// 函数位置映射在 GC 前后一致。
	lines := map[string]int{}
	for _, fn := range p.funcs {
		lines[fn.name] = fn.line
		ck(fn.line > 1, fmt.Sprintf("func %s line=%d", fn.name, fn.line))
		ck(len(fn.code) >= 2, fmt.Sprintf("func %s too few instructions", fn.name))
	}
	// 再次编译：字节码逐字节确定。
	p2, err := compileProgram(src)
	ck(err == nil, fmt.Sprintf("recompile: %v", err))
	if err == nil {
		ck(p2.dump() == dump, "bytecode compilation is not deterministic")
	}

	bad := []struct {
		name string
		src  string
		want string
	}{
		{"unknown", badUnknown, `unknown function "nope"`},
		{"arity", badArity, "want 1 args, got 2"},
		{"stmt", badStmt, "unsupported statement *ast.ForStmt"},
		{"literal", badLiteral, "unsupported literal STRING"},
		{"noreturn", badNoReturn, "missing final return"},
		{"ident", badIdent, `unknown identifier "zzz"`},
		{"dup", badDup, `duplicate function "f"`},
	}
	for _, bc := range bad {
		_, cerr := compileProgram(bc.src)
		ck(cerr != nil, fmt.Sprintf("%s: expected compile error, got nil", bc.name))
		if cerr != nil {
			ck(strings.Contains(cerr.Error(), bc.want),
				fmt.Sprintf("%s: error=%q missing %q", bc.name, cerr.Error(), bc.want))
		}
	}

	m := &machine{p: p, maxSteps: 2_000_000, maxDepth: 200}
	rounds := tortureRounds(6)
	rng := rand.New(rand.NewSource(uafSeed))
	for r := 0; r < rounds; r++ {
		for n := int64(0); n <= 14; n++ {
			got, cerr := m.callByName("fib", []int64{n})
			ck(cerr == nil, fmt.Sprintf("round %d fib(%d): %v", r, n, cerr))
			want := refFib(n)
			ck(got == want, fmt.Sprintf("round %d fib(%d)=%d want %d", r, n, got, want))
		}
		for i := 0; i < 40; i++ {
			x := rng.Int63n(81) - 40
			y := rng.Int63n(81) - 40
			if y == 0 {
				y = 3
			}
			cases := []struct {
				fn   string
				args []int64
				want int64
			}{
				{"poly", []int64{x}, refPoly(x)},
				{"clamp", []int64{y * 3}, refClamp(y * 3)},
				{"abs", []int64{x}, refAbs(x)},
				{"nested", []int64{x, y * 3}, refNested(x, y*3)},
				{"div", []int64{x, y}, x / y},
				{"rem", []int64{x, y}, x % y},
			}
			for _, c := range cases {
				got, cerr := m.callByName(c.fn, c.args)
				ck(cerr == nil, fmt.Sprintf("round %d %s%v: %v", r, c.fn, c.args, cerr))
				ck(got == c.want, fmt.Sprintf("round %d %s%v=%d want %d", r, c.fn, c.args, got, c.want))
			}
		}
		_, cerr := m.callByName("div", []int64{7, 0})
		ck(cerr != nil && strings.Contains(cerr.Error(), "division by zero"),
			fmt.Sprintf("round %d div by zero error=%v", r, cerr))
		_, cerr = m.callByName("rem", []int64{7, 0})
		ck(cerr != nil && strings.Contains(cerr.Error(), "division by zero"),
			fmt.Sprintf("round %d rem by zero error=%v", r, cerr))
		_, cerr = m.callByName("inf", []int64{1})
		ck(cerr != nil && strings.Contains(cerr.Error(), "call depth exceeded"),
			fmt.Sprintf("round %d infinite recursion error=%v", r, cerr))
		_, cerr = m.callByName("nosuch", nil)
		ck(cerr != nil && strings.Contains(cerr.Error(), "unknown function"),
			fmt.Sprintf("round %d unknown function error=%v", r, cerr))
		m.maxSteps = 10
		_, cerr = m.callByName("fib", []int64{10})
		ck(cerr != nil && strings.Contains(cerr.Error(), "step limit"),
			fmt.Sprintf("round %d step limit error=%v", r, cerr))
		m.maxSteps = 2_000_000

		gcStress()

		// GC 后字节码、函数表与 AST 位置仍可用，重放结果一致。
		ck(livePgm == p, "program lost after GC")
		ck(p.dump() == dump, "bytecode changed after GC")
		for name, line := range lines {
			fn := p.funcs[p.index[name]]
			ck(fn.line == line && p.fset.Position(fn.pos).Line == line,
				fmt.Sprintf("round %d func %s position changed after GC", r, name))
		}
		ck(p.file.Name != nil && p.file.Name.Name == "vm", "AST lost after GC")
		for n := int64(0); n <= 12; n++ {
			v, e := m.callByName("fib", []int64{n})
			ck(e == nil && v == refFib(n), fmt.Sprintf("round %d replay fib(%d)=(%d,%v)", r, n, v, e))
		}
		fixed := []struct {
			fn   string
			args []int64
			want int64
		}{
			{"poly", []int64{3}, refPoly(3)},
			{"clamp", []int64{250}, refClamp(250)},
			{"abs", []int64{-9}, refAbs(-9)},
			{"nested", []int64{5, -7}, refNested(5, -7)},
			{"div", []int64{21, 4}, 5},
			{"rem", []int64{21, 4}, 1},
		}
		for _, c := range fixed {
			v, e := m.callByName(c.fn, c.args)
			ck(e == nil && v == c.want, fmt.Sprintf("round %d replay %s%v=(%d,%v) want %d", r, c.fn, c.args, v, e, c.want))
		}
	}

	finish("l16_bytecode_vm")
}
