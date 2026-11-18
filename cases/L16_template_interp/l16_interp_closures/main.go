// L16: 基于 go/parser 的闭包解释器：函数字面量捕获环境、高阶函数、可变捕获变量、
// 递归、调用深度/步数限制；解释结果与编译期 Go 参考实现交叉校验，跨 GC 重放。
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
	"sync/atomic"
)

const src = `package closure

func makeAdder(n int) func(int) int {
	return func(x int) int { return x + n }
}

func apply(f func(int) int, v int) int {
	return f(v)
}

func compose(f func(int) int, g func(int) int) func(int) int {
	return func(x int) int { return f(g(x)) }
}

func counter(start int) func(int) int {
	c := start
	return func(d int) int {
		c = c + d
		return c
	}
}

func inc(x int) int { return x + 1 }

func dbl(x int) int { return x * 2 }

func sumTo(n int) int {
	if n == 0 {
		return 0
	}
	return n + sumTo(n-1)
}

func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func choose(flag int, a func(int) int, b func(int) int) func(int) int {
	if flag == 0 {
		return a
	}
	return b
}

func twice(f func(int) int, x int) int {
	return f(f(x))
}

func callInt(n int) int { return n(1) }

func unknown() int { return zzz }

func divZero(a int, b int) int { return a / b }

func failArity(x int) int { return x }

func spin(n int) int {
	for n > 0 {
	}
	return n
}

func loop(n int) int { return loop(n+1) }
`

var (
	failed  int32
	gcSink  [][]byte
	liveAST []*ast.File
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
		b := make([]byte, 8192)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 64; i++ {
		_ = make([]*closure, 64)
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
	if n > 20000 {
		n = 20000
	}
	return n
}

// ---------- 解释器 ----------

type kind uint8

const (
	kInt kind = iota
	kBool
	kFunc
)

type value struct {
	k  kind
	i  int64
	b  bool
	fn *closure
}

func num(n int64) value    { return value{k: kInt, i: n} }
func boolean(b bool) value { return value{k: kBool, b: b} }
func fnv(c *closure) value { return value{k: kFunc, fn: c} }

func (v value) truthy() bool {
	switch v.k {
	case kBool:
		return v.b
	case kInt:
		return v.i != 0
	case kFunc:
		return v.fn != nil
	}
	return false
}

type closure struct {
	name   string
	params []string
	body   *ast.BlockStmt
	env    *env
}

type env struct {
	vars   map[string]*value
	parent *env
}

func newEnv(parent *env) *env { return &env{vars: map[string]*value{}, parent: parent} }

func (e *env) define(name string, v value) {
	cell := new(value)
	*cell = v
	e.vars[name] = cell
}

func (e *env) lookup(name string) (*value, bool) {
	for s := e; s != nil; s = s.parent {
		if c, ok := s.vars[name]; ok {
			return c, true
		}
	}
	return nil, false
}

type interp struct {
	fset     *token.FileSet
	file     *ast.File
	global   *env
	pos      map[string]int
	steps    int
	maxSteps int
	depth    int
	maxDepth int
}

func paramNames(fl *ast.FieldList) []string {
	var out []string
	if fl == nil {
		return out
	}
	for _, f := range fl.List {
		for _, n := range f.Names {
			out = append(out, n.Name)
		}
	}
	return out
}

func newInterp(fset *token.FileSet, file *ast.File) *interp {
	in := &interp{fset: fset, file: file, global: newEnv(nil), pos: map[string]int{}, maxSteps: 200000, maxDepth: 200}
	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil || fd.Recv != nil {
			continue
		}
		c := &closure{name: fd.Name.Name, params: paramNames(fd.Type.Params), body: fd.Body, env: in.global}
		in.global.define(fd.Name.Name, fnv(c))
		in.pos[fd.Name.Name] = fset.Position(fd.Pos()).Line
	}
	return in
}

func (in *interp) line(pos token.Pos) int {
	if !pos.IsValid() {
		return 0
	}
	return in.fset.Position(pos).Line
}

func (in *interp) tick(pos token.Pos) error {
	in.steps++
	if in.steps > in.maxSteps {
		return fmt.Errorf("line %d: step limit exceeded", in.line(pos))
	}
	return nil
}

func (in *interp) eval(e ast.Expr, e2 *env) (value, error) {
	if err := in.tick(e.Pos()); err != nil {
		return value{}, err
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.INT {
			return value{}, fmt.Errorf("line %d: unsupported literal %q", in.line(x.Pos()), x.Value)
		}
		n, err := strconv.ParseInt(x.Value, 0, 64)
		if err != nil {
			return value{}, fmt.Errorf("line %d: bad int %q", in.line(x.Pos()), x.Value)
		}
		return num(n), nil
	case *ast.Ident:
		cell, ok := e2.lookup(x.Name)
		if !ok {
			return value{}, fmt.Errorf("line %d: unknown identifier %q", in.line(x.Pos()), x.Name)
		}
		return *cell, nil
	case *ast.ParenExpr:
		return in.eval(x.X, e2)
	case *ast.UnaryExpr:
		v, err := in.eval(x.X, e2)
		if err != nil {
			return value{}, err
		}
		switch x.Op {
		case token.SUB:
			if v.k != kInt {
				return value{}, fmt.Errorf("line %d: unary - needs int", in.line(x.Pos()))
			}
			return num(-v.i), nil
		case token.ADD:
			if v.k != kInt {
				return value{}, fmt.Errorf("line %d: unary + needs int", in.line(x.Pos()))
			}
			return v, nil
		case token.NOT:
			if v.k != kBool {
				return value{}, fmt.Errorf("line %d: ! needs bool", in.line(x.Pos()))
			}
			return boolean(!v.b), nil
		default:
			return value{}, fmt.Errorf("line %d: unsupported unary %v", in.line(x.Pos()), x.Op)
		}
	case *ast.BinaryExpr:
		if x.Op == token.LAND || x.Op == token.LOR {
			l, err := in.eval(x.X, e2)
			if err != nil {
				return value{}, err
			}
			if l.k != kBool {
				return value{}, fmt.Errorf("line %d: %v needs bool", in.line(x.Pos()), x.Op)
			}
			if x.Op == token.LAND && !l.b {
				return boolean(false), nil
			}
			if x.Op == token.LOR && l.b {
				return boolean(true), nil
			}
			r, err := in.eval(x.Y, e2)
			if err != nil {
				return value{}, err
			}
			if r.k != kBool {
				return value{}, fmt.Errorf("line %d: %v needs bool", in.line(x.Pos()), x.Op)
			}
			return boolean(r.b), nil
		}
		l, err := in.eval(x.X, e2)
		if err != nil {
			return value{}, err
		}
		r, err := in.eval(x.Y, e2)
		if err != nil {
			return value{}, err
		}
		if l.k == kBool && r.k == kBool {
			switch x.Op {
			case token.EQL:
				return boolean(l.b == r.b), nil
			case token.NEQ:
				return boolean(l.b != r.b), nil
			default:
				return value{}, fmt.Errorf("line %d: unsupported bool op %v", in.line(x.Pos()), x.Op)
			}
		}
		if l.k != kInt || r.k != kInt {
			return value{}, fmt.Errorf("line %d: mismatched operands for %v", in.line(x.Pos()), x.Op)
		}
		switch x.Op {
		case token.ADD:
			return num(l.i + r.i), nil
		case token.SUB:
			return num(l.i - r.i), nil
		case token.MUL:
			return num(l.i * r.i), nil
		case token.QUO:
			if r.i == 0 {
				return value{}, fmt.Errorf("line %d: division by zero", in.line(x.Pos()))
			}
			return num(l.i / r.i), nil
		case token.REM:
			if r.i == 0 {
				return value{}, fmt.Errorf("line %d: division by zero", in.line(x.Pos()))
			}
			return num(l.i % r.i), nil
		case token.EQL:
			return boolean(l.i == r.i), nil
		case token.NEQ:
			return boolean(l.i != r.i), nil
		case token.LSS:
			return boolean(l.i < r.i), nil
		case token.LEQ:
			return boolean(l.i <= r.i), nil
		case token.GTR:
			return boolean(l.i > r.i), nil
		case token.GEQ:
			return boolean(l.i >= r.i), nil
		default:
			return value{}, fmt.Errorf("line %d: unsupported binary op %v", in.line(x.Pos()), x.Op)
		}
	case *ast.CallExpr:
		fv, err := in.eval(x.Fun, e2)
		if err != nil {
			return value{}, err
		}
		if fv.k != kFunc {
			return value{}, fmt.Errorf("line %d: not a function in call", in.line(x.Pos()))
		}
		args := make([]value, 0, len(x.Args))
		for _, a := range x.Args {
			av, err := in.eval(a, e2)
			if err != nil {
				return value{}, err
			}
			args = append(args, av)
		}
		return in.callValue(fv, args, x.Pos())
	case *ast.FuncLit:
		return fnv(&closure{name: "<func>", params: paramNames(x.Type.Params), body: x.Body, env: e2}), nil
	default:
		return value{}, fmt.Errorf("line %d: unsupported expression %T", in.line(e.Pos()), e)
	}
}

func (in *interp) callValue(fv value, args []value, pos token.Pos) (value, error) {
	if in.depth == 0 {
		in.steps = 0
	}
	if fv.k != kFunc || fv.fn == nil {
		return value{}, fmt.Errorf("line %d: not a function", in.line(pos))
	}
	fn := fv.fn
	if len(args) != len(fn.params) {
		return value{}, fmt.Errorf("line %d: %s expects %d args, got %d", in.line(pos), fn.name, len(fn.params), len(args))
	}
	if in.depth >= in.maxDepth {
		return value{}, fmt.Errorf("line %d: call depth limit exceeded", in.line(pos))
	}
	callEnv := newEnv(fn.env)
	for i, p := range fn.params {
		callEnv.define(p, args[i])
	}
	in.depth++
	defer func() { in.depth-- }()
	v, hasRet, err := in.execBlock(fn.body, callEnv)
	if err != nil {
		return value{}, err
	}
	if !hasRet {
		return value{}, fmt.Errorf("line %d: %s returned no value", in.line(fn.body.Pos()), fn.name)
	}
	return v, nil
}

func (in *interp) execBlock(b *ast.BlockStmt, parent *env) (value, bool, error) {
	e := newEnv(parent)
	for _, st := range b.List {
		v, hasRet, err := in.execStmt(st, e)
		if err != nil {
			return value{}, false, err
		}
		if hasRet {
			return v, true, nil
		}
	}
	return value{}, false, nil
}

func (in *interp) assign(lhs ast.Expr, v value, define bool, e *env, pos token.Pos) error {
	id, ok := lhs.(*ast.Ident)
	if !ok {
		return fmt.Errorf("line %d: unsupported assignment target %T", in.line(pos), lhs)
	}
	if define {
		e.define(id.Name, v)
		return nil
	}
	cell, ok := e.lookup(id.Name)
	if !ok {
		return fmt.Errorf("line %d: assign to unknown %q", in.line(pos), id.Name)
	}
	*cell = v
	return nil
}

func (in *interp) execStmt(st ast.Stmt, e *env) (value, bool, error) {
	if err := in.tick(st.Pos()); err != nil {
		return value{}, false, err
	}
	switch s := st.(type) {
	case *ast.BlockStmt:
		return in.execBlock(s, e)
	case *ast.EmptyStmt:
		return value{}, false, nil
	case *ast.ExprStmt:
		_, err := in.eval(s.X, e)
		return value{}, false, err
	case *ast.AssignStmt:
		if len(s.Lhs) != 1 || len(s.Rhs) != 1 {
			return value{}, false, fmt.Errorf("line %d: multi-assign unsupported", in.line(s.Pos()))
		}
		v, err := in.eval(s.Rhs[0], e)
		if err != nil {
			return value{}, false, err
		}
		if s.Tok == token.DEFINE {
			return value{}, false, in.assign(s.Lhs[0], v, true, e, s.Pos())
		}
		return value{}, false, in.assign(s.Lhs[0], v, false, e, s.Pos())
	case *ast.IncDecStmt:
		id, ok := s.X.(*ast.Ident)
		if !ok {
			return value{}, false, fmt.Errorf("line %d: unsupported inc/dec", in.line(s.Pos()))
		}
		cell, ok := e.lookup(id.Name)
		if !ok || cell.k != kInt {
			return value{}, false, fmt.Errorf("line %d: inc/dec on non-int", in.line(s.Pos()))
		}
		if s.Tok == token.INC {
			cell.i++
		} else {
			cell.i--
		}
		return value{}, false, nil
	case *ast.ReturnStmt:
		if len(s.Results) != 1 {
			return value{}, false, fmt.Errorf("line %d: return needs one value", in.line(s.Pos()))
		}
		v, err := in.eval(s.Results[0], e)
		if err != nil {
			return value{}, false, err
		}
		return v, true, nil
	case *ast.IfStmt:
		if s.Init != nil {
			if _, _, err := in.execStmt(s.Init, e); err != nil {
				return value{}, false, err
			}
		}
		cond, err := in.eval(s.Cond, e)
		if err != nil {
			return value{}, false, err
		}
		if cond.truthy() {
			return in.execBlock(s.Body, e)
		}
		if s.Else != nil {
			if blk, ok := s.Else.(*ast.BlockStmt); ok {
				return in.execBlock(blk, e)
			}
			return in.execStmt(s.Else, e)
		}
		return value{}, false, nil
	case *ast.ForStmt:
		if s.Init != nil {
			if _, _, err := in.execStmt(s.Init, e); err != nil {
				return value{}, false, err
			}
		}
		for {
			if s.Cond != nil {
				cond, err := in.eval(s.Cond, e)
				if err != nil {
					return value{}, false, err
				}
				if !cond.truthy() {
					break
				}
			}
			v, hasRet, err := in.execBlock(s.Body, e)
			if err != nil {
				return value{}, false, err
			}
			if hasRet {
				return v, true, nil
			}
			if s.Post != nil {
				if _, _, err := in.execStmt(s.Post, e); err != nil {
					return value{}, false, err
				}
			}
		}
		return value{}, false, nil
	default:
		return value{}, false, fmt.Errorf("line %d: unsupported statement %T", in.line(st.Pos()), st)
	}
}

func (in *interp) callName(name string, args ...value) (value, error) {
	cell, ok := in.global.lookup(name)
	if !ok {
		return value{}, fmt.Errorf("unknown function %q", name)
	}
	in.steps = 0
	return in.callValue(*cell, args, in.file.Pos())
}

func (in *interp) globalValue(name string) (value, error) {
	cell, ok := in.global.lookup(name)
	if !ok {
		return value{}, fmt.Errorf("unknown function %q", name)
	}
	return *cell, nil
}

// ---------- 参考实现 ----------

func refFib(n int64) int64 {
	if n < 2 {
		return n
	}
	return refFib(n-1) + refFib(n-2)
}

func refSum(n int64) int64 {
	var s int64
	for i := int64(1); i <= n; i++ {
		s += i
	}
	return s
}

func refCounter(start int64) func(int64) int64 {
	c := start
	return func(d int64) int64 {
		c += d
		return c
	}
}

func expectInt(tag string, v value, err error, want int64) {
	if err != nil {
		ck(false, fmt.Sprintf("%s err=%v", tag, err))
		return
	}
	ck(v.k == kInt && v.i == want, fmt.Sprintf("%s got kind=%v i=%d want %d", tag, v.k, v.i, want))
}

func main() {
	uafSeed = envSeed()
	rounds := tortureRounds(24)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "closure.go", src, 0)
	ck(err == nil, fmt.Sprintf("parse err=%v", err))
	if file == nil {
		finish("l16_interp_closures")
		return
	}
	liveAST = append(liveAST, file)
	in := newInterp(fset, file)

	// 位置表与函数表。
	wantLines := map[string]int{"makeAdder": 3, "counter": 15, "sumTo": 27, "fib": 34, "choose": 41, "twice": 48, "spin": 60, "loop": 66}
	for name, line := range wantLines {
		ck(in.pos[name] == line, fmt.Sprintf("line of %s=%d want %d", name, in.pos[name], line))
		_, ok := in.global.lookup(name)
		ck(ok, fmt.Sprintf("global func %s missing", name))
	}
	if _, ok := in.global.lookup("nosuch"); ok {
		ck(false, "unexpected global nosuch")
	}

	// 闭包基本语义：捕获参数、高阶函数、函数组合。
	ma7, err := in.callName("makeAdder", num(7))
	ck(err == nil && ma7.k == kFunc, fmt.Sprintf("makeAdder err=%v", err))
	if ma7.k == kFunc {
		v, err := in.callValue(ma7, []value{num(5)}, file.Pos())
		expectInt("makeAdder(7)(5)", v, err, 12)
	}
	ma10, _ := in.callName("makeAdder", num(10))
	v, err := in.callName("apply", ma10, num(32))
	expectInt("apply(makeAdder(10),32)", v, err, 42)

	incV, _ := in.globalValue("inc")
	dblV, _ := in.globalValue("dbl")
	cmp, err := in.callName("compose", incV, dblV)
	ck(err == nil && cmp.k == kFunc, fmt.Sprintf("compose err=%v", err))
	if cmp.k == kFunc {
		v, err := in.callValue(cmp, []value{num(5)}, file.Pos())
		expectInt("compose(inc,dbl)(5)", v, err, 11)
	}

	// 可变捕获变量：两个 counter 实例状态独立。
	c1, err := in.callName("counter", num(0))
	ck(err == nil && c1.k == kFunc, fmt.Sprintf("counter1 err=%v", err))
	c2, err := in.callName("counter", num(100))
	ck(err == nil && c2.k == kFunc, fmt.Sprintf("counter2 err=%v", err))
	rc1 := refCounter(0)
	rc2 := refCounter(100)
	for _, d := range []int64{3, 4, 0} {
		v, err := in.callValue(c1, []value{num(d)}, file.Pos())
		expectInt(fmt.Sprintf("c1(%d)", d), v, err, rc1(d))
	}
	for _, d := range []int64{1, -5} {
		v, err := in.callValue(c2, []value{num(d)}, file.Pos())
		expectInt(fmt.Sprintf("c2(%d)", d), v, err, rc2(d))
	}
	v, err = in.callValue(c1, []value{num(0)}, file.Pos())
	expectInt("c1 after c2", v, err, rc1(0))

	// 返回函数值 / 函数作为参数。
	choice0, err := in.callName("choose", num(0), incV, dblV)
	ck(err == nil && choice0.k == kFunc, fmt.Sprintf("choose0 err=%v", err))
	if choice0.k == kFunc {
		v, err := in.callValue(choice0, []value{num(5)}, file.Pos())
		expectInt("choose0(5)", v, err, 6)
	}
	choice1, _ := in.callName("choose", num(1), incV, dblV)
	if choice1.k == kFunc {
		v, err := in.callValue(choice1, []value{num(5)}, file.Pos())
		expectInt("choose1(5)", v, err, 10)
	}
	v, err = in.callName("twice", incV, num(5))
	expectInt("twice(inc,5)", v, err, 7)
	ma3, _ := in.callName("makeAdder", num(3))
	v, err = in.callName("twice", ma3, num(5))
	expectInt("twice(makeAdder(3),5)", v, err, 11)

	// 递归。
	v, err = in.callName("sumTo", num(10))
	expectInt("sumTo(10)", v, err, refSum(10))
	v, err = in.callName("fib", num(10))
	expectInt("fib(10)", v, err, refFib(10))

	// 错误路径：非函数、arity、未知标识符、除零、步数/深度。
	_, err = in.callName("callInt", num(1))
	ck(err != nil && contains(err.Error(), "not a function"), fmt.Sprintf("callInt err=%v", err))
	_, err = in.callName("failArity")
	ck(err != nil && contains(err.Error(), "expects 1 args"), fmt.Sprintf("arity err=%v", err))
	_, err = in.callName("failArity", num(1), num(2))
	ck(err != nil && contains(err.Error(), "expects 1 args"), fmt.Sprintf("arity2 err=%v", err))
	_, err = in.callName("unknown")
	ck(err != nil && contains(err.Error(), "unknown identifier"), fmt.Sprintf("unknown err=%v", err))
	_, err = in.callName("divZero", num(1), num(0))
	ck(err != nil && contains(err.Error(), "division by zero"), fmt.Sprintf("divZero err=%v", err))
	_, err = in.callName("spin", num(1))
	ck(err != nil && contains(err.Error(), "step limit"), fmt.Sprintf("spin err=%v", err))
	_, err = in.callName("loop", num(0))
	ck(err != nil && contains(err.Error(), "depth limit"), fmt.Sprintf("loop err=%v", err))
	_, err = in.callName("nosuch")
	ck(err != nil && contains(err.Error(), "unknown function"), fmt.Sprintf("nosuch err=%v", err))

	// GC 后闭包/捕获变量/AST 仍有效。
	gcStress()
	v, err = in.callValue(c1, []value{num(1)}, file.Pos())
	expectInt("c1 after GC", v, err, rc1(1))
	v, err = in.callValue(c2, []value{num(0)}, file.Pos())
	expectInt("c2 after GC", v, err, rc2(0))
	v, err = in.callName("fib", num(12))
	expectInt("fib(12) after GC", v, err, refFib(12))
	for name, line := range wantLines {
		ck(in.pos[name] == line, fmt.Sprintf("post-GC line of %s changed", name))
	}
	ck(len(liveAST) == 1 && liveAST[0] == file, "AST lost after GC")

	// TORTURE_ROUNDS：随机顺序重放递归/高阶函数/闭包状态。
	rng := rand.New(rand.NewSource(uafSeed))
	for round := 0; round < rounds; round++ {
		n := int64(1 + (int(uafSeed)+round)%10)
		checks := rng.Perm(4)
		for _, c := range checks {
			switch c {
			case 0:
				v, err := in.callName("sumTo", num(n))
				expectInt(fmt.Sprintf("round %d sumTo(%d)", round, n), v, err, refSum(n))
			case 1:
				v, err := in.callName("fib", num(n))
				expectInt(fmt.Sprintf("round %d fib(%d)", round, n), v, err, refFib(n))
			case 2:
				v, err := in.callName("twice", incV, num(n))
				expectInt(fmt.Sprintf("round %d twice(inc,%d)", round, n), v, err, n+2)
			case 3:
				d := n % 5
				v, err := in.callValue(c1, []value{num(d)}, file.Pos())
				expectInt(fmt.Sprintf("round %d c1(%d)", round, d), v, err, rc1(d))
			}
		}
		if round%4 == 0 {
			gcStress()
		}
	}
	// 最终状态仍与参考实现一致。
	v, err = in.callValue(c1, []value{num(7)}, file.Pos())
	expectInt("final c1", v, err, rc1(7))
	ck(len(file.Decls) == 16, fmt.Sprintf("decls after GC=%d", len(file.Decls)))

	finish("l16_interp_closures")
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
