// L16: 解释器闭包与循环变量语义边界：每轮 for 变量独立绑定、闭包捕获环境、
// 互递归/自递归、闭包可变捕获与高阶组合；结果与编译期 Go 参考实现交叉校验，跨 GC。
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

const src = `package closure

func loopCapture(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		f := func() int { return i }
		total = total*10 + f()
	}
	return total
}

func adders(n int) func(int) int {
	return func(x int) int { return x + n }
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

func even(n int) bool {
	if n == 0 { return true }
	return odd(n - 1)
}

func odd(n int) bool {
	if n == 0 { return false }
	return even(n - 1)
}

func fact(n int) int {
	var f func(int) int
	f = func(x int) int {
		if x <= 1 { return 1 }
		return x * f(x - 1)
	}
	return f(n)
}

func combine(n int) int {
	a := adders(n)
	b := adders(n * 10)
	bump := counter(0)
	bump(5)
	return a(1) + b(2) + compose(a, b)(0)
}
`

var (
	failed  int32
	gcSink  [][]byte
	liveAST []*ast.File
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
		b := make([]byte, 8192)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 128; i++ {
		_ = &env{vars: map[string]value{"x": {k: kInt, i: int64(i)}}}
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
	if n > 50000 {
		n = 50000
	}
	return n
}

// ---------- 值与环境 ----------

type kind int

const (
	kNil kind = iota
	kInt
	kBool
	kFunc
)

type value struct {
	k  kind
	i  int64
	b  bool
	fn *closure
}

type closure struct {
	decl *ast.FuncDecl
	lit  *ast.FuncLit
	env  *env
}

func intVal(n int64) value    { return value{k: kInt, i: n} }
func boolVal(b bool) value    { return value{k: kBool, b: b} }
func funVal(c *closure) value { return value{k: kFunc, fn: c} }

func (v value) str() string {
	switch v.k {
	case kInt:
		return strconv.FormatInt(v.i, 10)
	case kBool:
		return strconv.FormatBool(v.b)
	case kFunc:
		if v.fn == nil {
			return "<nil func>"
		}
		return "<func>"
	default:
		return "<nil>"
	}
}

type env struct {
	vars   map[string]value
	parent *env
}

func newEnv(parent *env) *env {
	return &env{vars: map[string]value{}, parent: parent}
}

func (e *env) lookup(name string) (value, bool) {
	for s := e; s != nil; s = s.parent {
		if v, ok := s.vars[name]; ok {
			return v, true
		}
	}
	return value{}, false
}

func (e *env) assign(name string, v value) bool {
	for s := e; s != nil; s = s.parent {
		if _, ok := s.vars[name]; ok {
			s.vars[name] = v
			return true
		}
	}
	return false
}

// ---------- 解释器 ----------

type interp struct {
	fset    *token.FileSet
	globals map[string]*ast.FuncDecl
	global  *env
	steps   int
	maxStep int
	depth   int
	maxDep  int
}

func newInterp(fset *token.FileSet, file *ast.File) *interp {
	in := &interp{fset: fset, globals: map[string]*ast.FuncDecl{}, global: newEnv(nil), maxStep: 2000000, maxDep: 256}
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name != nil {
			in.globals[fd.Name.Name] = fd
		}
	}
	return in
}

func (in *interp) tick(pos token.Pos) error {
	in.steps++
	if in.steps > in.maxStep {
		return fmt.Errorf("%s: step limit exceeded", in.fset.Position(pos))
	}
	return nil
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

func (in *interp) callValue(fv value, args []value, pos token.Pos) (value, error) {
	if fv.k != kFunc || fv.fn == nil {
		return value{}, fmt.Errorf("%s: call of non-function %s", in.fset.Position(pos), fv.str())
	}
	if in.depth >= in.maxDep {
		return value{}, fmt.Errorf("%s: call depth exceeded", in.fset.Position(pos))
	}
	fn := fv.fn
	var child *env
	var body *ast.BlockStmt
	var params *ast.FieldList
	if fn.decl != nil {
		child = newEnv(in.global)
		body = fn.decl.Body
		params = fn.decl.Type.Params
	} else {
		child = newEnv(fn.env)
		body = fn.lit.Body
		params = fn.lit.Type.Params
	}
	names := paramNames(params)
	if len(names) != len(args) {
		return value{}, fmt.Errorf("%s: arity mismatch got %d want %d", in.fset.Position(pos), len(args), len(names))
	}
	for i, n := range names {
		child.vars[n] = args[i]
	}
	in.depth++
	defer func() { in.depth-- }()
	bodyEnv := newEnv(child)
	res, ctrl, err := in.execBlockIn(body, bodyEnv)
	if err != nil {
		return value{}, err
	}
	if ctrl != ctrlReturn {
		return value{k: kNil}, nil
	}
	return res, nil
}

func (in *interp) eval(e ast.Expr, e2 *env) (value, error) {
	if err := in.tick(e.Pos()); err != nil {
		return value{}, err
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		switch x.Kind {
		case token.INT:
			n, err := strconv.ParseInt(x.Value, 0, 64)
			if err != nil {
				return value{}, fmt.Errorf("%s: bad int %q", in.fset.Position(x.Pos()), x.Value)
			}
			return intVal(n), nil
		}
		return value{}, fmt.Errorf("%s: unsupported literal %s", in.fset.Position(x.Pos()), x.Value)
	case *ast.Ident:
		if v, ok := e2.lookup(x.Name); ok {
			return v, nil
		}
		switch x.Name {
		case "true":
			return boolVal(true), nil
		case "false":
			return boolVal(false), nil
		}
		if fd, ok := in.globals[x.Name]; ok {
			return funVal(&closure{decl: fd}), nil
		}
		return value{}, fmt.Errorf("%s: undefined %s", in.fset.Position(x.Pos()), x.Name)
	case *ast.ParenExpr:
		return in.eval(x.X, e2)
	case *ast.FuncLit:
		return funVal(&closure{lit: x, env: e2}), nil
	case *ast.UnaryExpr:
		v, err := in.eval(x.X, e2)
		if err != nil {
			return value{}, err
		}
		switch x.Op {
		case token.SUB:
			if v.k != kInt {
				return value{}, fmt.Errorf("%s: unary - on %s", in.fset.Position(x.Pos()), v.str())
			}
			return intVal(-v.i), nil
		case token.ADD:
			return v, nil
		case token.NOT:
			if v.k != kBool {
				return value{}, fmt.Errorf("%s: ! on %s", in.fset.Position(x.Pos()), v.str())
			}
			return boolVal(!v.b), nil
		}
		return value{}, fmt.Errorf("%s: unsupported unary %s", in.fset.Position(x.Pos()), x.Op)
	case *ast.BinaryExpr:
		l, err := in.eval(x.X, e2)
		if err != nil {
			return value{}, err
		}
		r, err := in.eval(x.Y, e2)
		if err != nil {
			return value{}, err
		}
		return binary(x.Op, l, r, in.fset.Position(x.Pos()))
	case *ast.CallExpr:
		fv, err := in.eval(x.Fun, e2)
		if err != nil {
			return value{}, err
		}
		args := make([]value, 0, len(x.Args))
		for _, a := range x.Args {
			v, err := in.eval(a, e2)
			if err != nil {
				return value{}, err
			}
			args = append(args, v)
		}
		return in.callValue(fv, args, x.Pos())
	}
	return value{}, fmt.Errorf("%s: unsupported expr %T", in.fset.Position(e.Pos()), e)
}

func binary(op token.Token, l, r value, pos token.Position) (value, error) {
	if op == token.EQL || op == token.NEQ {
		if l.k != r.k {
			return value{}, fmt.Errorf("%s: compare %s with %s", pos, l.str(), r.str())
		}
		eq := false
		switch l.k {
		case kInt:
			eq = l.i == r.i
		case kBool:
			eq = l.b == r.b
		default:
			return value{}, fmt.Errorf("%s: unsupported equality %T", pos, l)
		}
		if op == token.NEQ {
			eq = !eq
		}
		return boolVal(eq), nil
	}
	if l.k != kInt || r.k != kInt {
		return value{}, fmt.Errorf("%s: %s on %s/%s", pos, op, l.str(), r.str())
	}
	switch op {
	case token.ADD:
		return intVal(l.i + r.i), nil
	case token.SUB:
		return intVal(l.i - r.i), nil
	case token.MUL:
		return intVal(l.i * r.i), nil
	case token.QUO:
		if r.i == 0 {
			return value{}, fmt.Errorf("%s: division by zero", pos)
		}
		return intVal(l.i / r.i), nil
	case token.REM:
		if r.i == 0 {
			return value{}, fmt.Errorf("%s: division by zero", pos)
		}
		return intVal(l.i % r.i), nil
	case token.LSS:
		return boolVal(l.i < r.i), nil
	case token.LEQ:
		return boolVal(l.i <= r.i), nil
	case token.GTR:
		return boolVal(l.i > r.i), nil
	case token.GEQ:
		return boolVal(l.i >= r.i), nil
	}
	return value{}, fmt.Errorf("%s: unsupported binary %s", pos, op)
}

type control int

const (
	ctrlNone control = iota
	ctrlReturn
)

func (in *interp) execBlockIn(b *ast.BlockStmt, e *env) (value, control, error) {
	if b == nil {
		return value{k: kNil}, ctrlNone, nil
	}
	for _, st := range b.List {
		res, ctrl, err := in.execStmt(st, e)
		if err != nil {
			return value{}, ctrlNone, err
		}
		if ctrl == ctrlReturn {
			return res, ctrl, nil
		}
	}
	return value{k: kNil}, ctrlNone, nil
}

func (in *interp) execStmt(st ast.Stmt, e *env) (value, control, error) {
	if err := in.tick(st.Pos()); err != nil {
		return value{}, ctrlNone, err
	}
	switch x := st.(type) {
	case *ast.ExprStmt:
		_, err := in.eval(x.X, e)
		return value{k: kNil}, ctrlNone, err
	case *ast.AssignStmt:
		if len(x.Lhs) != len(x.Rhs) {
			return value{}, ctrlNone, fmt.Errorf("%s: multi-assign unsupported", in.fset.Position(x.Pos()))
		}
		for i, lhs := range x.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok {
				return value{}, ctrlNone, fmt.Errorf("%s: unsupported lhs %T", in.fset.Position(lhs.Pos()), lhs)
			}
			v, err := in.eval(x.Rhs[i], e)
			if err != nil {
				return value{}, ctrlNone, err
			}
			if x.Tok == token.DEFINE {
				e.vars[id.Name] = v
				continue
			}
			if !e.assign(id.Name, v) {
				return value{}, ctrlNone, fmt.Errorf("%s: assign to undefined %s", in.fset.Position(id.Pos()), id.Name)
			}
		}
		return value{k: kNil}, ctrlNone, nil
	case *ast.IncDecStmt:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return value{}, ctrlNone, fmt.Errorf("%s: incdec non-ident", in.fset.Position(x.Pos()))
		}
		v, found := e.lookup(id.Name)
		if !found || v.k != kInt {
			return value{}, ctrlNone, fmt.Errorf("%s: incdec %s", in.fset.Position(id.Pos()), id.Name)
		}
		if x.Tok == token.INC {
			v.i++
		} else {
			v.i--
		}
		e.assign(id.Name, v)
		return value{k: kNil}, ctrlNone, nil
	case *ast.DeclStmt:
		gd, ok := x.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return value{}, ctrlNone, fmt.Errorf("%s: unsupported decl %T", in.fset.Position(x.Pos()), x.Decl)
		}
		for _, spec := range gd.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, n := range vs.Names {
				var v value
				if i < len(vs.Values) {
					var err error
					v, err = in.eval(vs.Values[i], e)
					if err != nil {
						return value{}, ctrlNone, err
					}
				}
				e.vars[n.Name] = v
			}
		}
		return value{k: kNil}, ctrlNone, nil
	case *ast.ReturnStmt:
		if len(x.Results) == 0 {
			return value{k: kNil}, ctrlReturn, nil
		}
		v, err := in.eval(x.Results[0], e)
		if err != nil {
			return value{}, ctrlNone, err
		}
		return v, ctrlReturn, nil
	case *ast.IfStmt:
		condEnv := newEnv(e)
		if x.Init != nil {
			if _, _, err := in.execStmt(x.Init, condEnv); err != nil {
				return value{}, ctrlNone, err
			}
		}
		c, err := in.eval(x.Cond, condEnv)
		if err != nil {
			return value{}, ctrlNone, err
		}
		if c.k != kBool {
			return value{}, ctrlNone, fmt.Errorf("%s: if condition %s", in.fset.Position(x.Cond.Pos()), c.str())
		}
		if c.b {
			return in.execBlockIn(x.Body, newEnv(condEnv))
		}
		if x.Else != nil {
			return in.execStmt(x.Else, condEnv)
		}
		return value{k: kNil}, ctrlNone, nil
	case *ast.BlockStmt:
		return in.execBlockIn(x, newEnv(e))
	case *ast.ForStmt:
		return in.execFor(x, e)
	}
	return value{}, ctrlNone, fmt.Errorf("%s: unsupported stmt %T", in.fset.Position(st.Pos()), st)
}

func (in *interp) execFor(x *ast.ForStmt, e *env) (value, control, error) {
	loopEnv := newEnv(e)
	var loopVars []string
	if x.Init != nil {
		if as, ok := x.Init.(*ast.AssignStmt); ok && as.Tok == token.DEFINE {
			for _, lhs := range as.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					loopVars = append(loopVars, id.Name)
				}
			}
		}
		if _, _, err := in.execStmt(x.Init, loopEnv); err != nil {
			return value{}, ctrlNone, err
		}
	}
	for iter := 0; ; iter++ {
		if err := in.tick(x.Pos()); err != nil {
			return value{}, ctrlNone, err
		}
		iterEnv := newEnv(loopEnv)
		for _, name := range loopVars {
			if v, ok := loopEnv.vars[name]; ok {
				iterEnv.vars[name] = v
			}
		}
		if x.Cond != nil {
			c, err := in.eval(x.Cond, iterEnv)
			if err != nil {
				return value{}, ctrlNone, err
			}
			if c.k != kBool {
				return value{}, ctrlNone, fmt.Errorf("%s: for condition %s", in.fset.Position(x.Cond.Pos()), c.str())
			}
			if !c.b {
				return value{k: kNil}, ctrlNone, nil
			}
		}
		res, ctrl, err := in.execBlockIn(x.Body, newEnv(iterEnv))
		if err != nil {
			return value{}, ctrlNone, err
		}
		if ctrl == ctrlReturn {
			return res, ctrl, nil
		}
		if x.Post != nil {
			if _, _, err := in.execStmt(x.Post, iterEnv); err != nil {
				return value{}, ctrlNone, err
			}
		}
		for _, name := range loopVars {
			if v, ok := iterEnv.vars[name]; ok {
				loopEnv.vars[name] = v
			}
		}
		if iter > 1000000 {
			return value{}, ctrlNone, fmt.Errorf("%s: for iteration limit", in.fset.Position(x.Pos()))
		}
	}
}

// ---------- 参考实现 ----------

func refLoopCapture(n int) int64 {
	total := int64(0)
	for i := 0; i < n; i++ {
		f := func() int { return i }
		total = total*10 + int64(f())
	}
	return total
}

func refAdders(n int64) func(int64) int64 {
	return func(x int64) int64 { return x + n }
}

func refCompose(f, g func(int64) int64) func(int64) int64 {
	return func(x int64) int64 { return f(g(x)) }
}

func refCounter(start int64) func(int64) int64 {
	c := start
	return func(d int64) int64 {
		c = c + d
		return c
	}
}

func refEven(n int64) bool {
	if n == 0 {
		return true
	}
	return refOdd(n - 1)
}

func refOdd(n int64) bool {
	if n == 0 {
		return false
	}
	return refEven(n - 1)
}

func refFact(n int64) int64 {
	var f func(int64) int64
	f = func(x int64) int64 {
		if x <= 1 {
			return 1
		}
		return x * f(x-1)
	}
	return f(n)
}

func refCombine(n int64) int64 {
	a := refAdders(n)
	b := refAdders(n * 10)
	bump := refCounter(0)
	bump(5)
	return a(1) + b(2) + refCompose(a, b)(0)
}

func expectInt(tag string, v value, err error, want int64) {
	if err != nil {
		ck(false, fmt.Sprintf("%s err=%v", tag, err))
		return
	}
	ck(v.k == kInt && v.i == want, fmt.Sprintf("%s=%s want %d", tag, v.str(), want))
}

func main() {
	uafSeed = envSeed()
	rng := rand.New(rand.NewSource(uafSeed))
	rounds := tortureRounds(12)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "closure.go", src, 0)
	ck(err == nil, fmt.Sprintf("parse: %v", err))
	if file == nil {
		finish("l16_interp_closure_loop")
		return
	}
	liveAST = append(liveAST, file)

	// 固定回归：每轮循环变量独立捕获；互递归与自递归；高阶组合。
	fixedN := []int64{0, 1, 3, 5}
	for _, n := range fixedN {
		in := newInterp(fset, file)
		expr, perr := parser.ParseExpr(fmt.Sprintf("loopCapture(%d)", n))
		ck(perr == nil, fmt.Sprintf("parse expr: %v", perr))
		if perr != nil {
			continue
		}
		v, err := in.eval(expr, in.global)
		expectInt(fmt.Sprintf("loopCapture(%d)", n), v, err, refLoopCapture(int(n)))
		in = newInterp(fset, file)
		expr, _ = parser.ParseExpr(fmt.Sprintf("fact(%d)", n))
		v, err = in.eval(expr, in.global)
		expectInt(fmt.Sprintf("fact(%d)", n), v, err, refFact(n))
		in = newInterp(fset, file)
		expr, _ = parser.ParseExpr(fmt.Sprintf("combine(%d)", n))
		v, err = in.eval(expr, in.global)
		expectInt(fmt.Sprintf("combine(%d)", n), v, err, refCombine(n))
	}
	for _, n := range []int64{0, 1, 2, 7, 10} {
		in := newInterp(fset, file)
		expr, _ := parser.ParseExpr(fmt.Sprintf("even(%d)", n))
		v, err := in.eval(expr, in.global)
		if err != nil {
			ck(false, fmt.Sprintf("even(%d) err=%v", n, err))
			continue
		}
		ck(v.k == kBool && v.b == refEven(n), fmt.Sprintf("even(%d)=%s", n, v.str()))
		in = newInterp(fset, file)
		expr, _ = parser.ParseExpr(fmt.Sprintf("odd(%d)", n))
		v, err = in.eval(expr, in.global)
		if err != nil {
			ck(false, fmt.Sprintf("odd(%d) err=%v", n, err))
			continue
		}
		ck(v.k == kBool && v.b == refOdd(n), fmt.Sprintf("odd(%d)=%s", n, v.str()))
	}

	// 返回的闭包捕获环境并可跨 GC 变更：counter 三次调用 5/12/19。
	in := newInterp(fset, file)
	counterClosure, err := in.callName("counter", []value{intVal(0)})
	ck(err == nil, fmt.Sprintf("counter construct err=%v", err))
	if err == nil {
		live = append(live, counterClosure)
		want := int64(0)
		for _, d := range []int64{5, 7, 7} {
			v, err := in.callValue(counterClosure, []value{intVal(d)}, token.NoPos)
			want += d
			expectInt(fmt.Sprintf("counter+%d", d), v, err, want)
		}
		// GC 后同一闭包仍保留可变环境。
		gcStress()
		v, err := in.callValue(counterClosure, []value{intVal(1)}, token.NoPos)
		expectInt("counter after GC", v, err, want+1)
	}

	// 错误路径：未定义名字、arity、nil 函数调用。
	if _, err := in.eval(mustExpr("zzz"), in.global); err == nil {
		ck(false, "undefined name must error")
	}
	adder, err := in.callName("adders", []value{intVal(2)})
	ck(err == nil, fmt.Sprintf("adders err=%v", err))
	if err == nil {
		live = append(live, adder)
		if _, err := in.callValue(adder, []value{intVal(1), intVal(2)}, token.NoPos); err == nil ||
			!strings.Contains(err.Error(), "arity") {
			ck(false, fmt.Sprintf("arity err=%v", err))
		}
	}
	if _, err := in.callValue(value{k: kNil}, nil, token.NoPos); err == nil {
		ck(false, "nil call must error")
	}

	// TORTURE_ROUNDS：随机 n 与检查顺序，解释结果与编译期参考实现一致。
	names := []string{"loopCapture", "fact", "combine", "even", "odd"}
	for round := 0; round < rounds; round++ {
		n := int64(1 + rng.Intn(6))
		perm := rng.Perm(len(names))
		for _, ni := range perm {
			name := names[ni]
			rin := newInterp(fset, file)
			expr, perr := parser.ParseExpr(fmt.Sprintf("%s(%d)", name, n))
			ck(perr == nil, fmt.Sprintf("round %d parse %s err=%v", round, name, perr))
			if perr != nil {
				continue
			}
			v, err := rin.eval(expr, rin.global)
			ck(err == nil, fmt.Sprintf("round %d %s(%d) err=%v", round, name, n, err))
			if err != nil {
				continue
			}
			switch name {
			case "loopCapture":
				ck(v.k == kInt && v.i == refLoopCapture(int(n)), fmt.Sprintf("round %d loopCapture(%d)=%s", round, n, v.str()))
			case "fact":
				ck(v.k == kInt && v.i == refFact(n), fmt.Sprintf("round %d fact(%d)=%s", round, n, v.str()))
			case "combine":
				ck(v.k == kInt && v.i == refCombine(n), fmt.Sprintf("round %d combine(%d)=%s", round, n, v.str()))
			case "even":
				ck(v.k == kBool && v.b == refEven(n), fmt.Sprintf("round %d even(%d)=%s", round, n, v.str()))
			case "odd":
				ck(v.k == kBool && v.b == refOdd(n), fmt.Sprintf("round %d odd(%d)=%s", round, n, v.str()))
			}
		}
		// 每轮重新构造闭包并立即调用，随后 GC 再调用。
		rin := newInterp(fset, file)
		cl, err := rin.callName("counter", []value{intVal(n)})
		ck(err == nil, fmt.Sprintf("round %d counter err=%v", round, err))
		if err == nil {
			v, err := rin.callValue(cl, []value{intVal(3)}, token.NoPos)
			expectInt(fmt.Sprintf("round %d counter", round), v, err, n+3)
			live = append(live, cl)
		}
		if round%3 == 0 {
			gcStress()
		}
		// AST 与保留闭包在 GC 后仍可执行。
		rin2 := newInterp(fset, file)
		expr, _ := parser.ParseExpr(fmt.Sprintf("fact(%d)", 1+rng.Intn(5)))
		_, err = rin2.eval(expr, rin2.global)
		ck(err == nil, fmt.Sprintf("round %d post-GC eval err=%v", round, err))
	}

	finish("l16_interp_closure_loop")
}

func mustExpr(s string) ast.Expr {
	e, err := parser.ParseExpr(s)
	if err != nil {
		panic(err)
	}
	return e
}

// callName 通过全局函数名构造闭包值。
func (in *interp) callName(name string, args []value) (value, error) {
	fd, ok := in.globals[name]
	if !ok {
		return value{}, fmt.Errorf("%s: undefined function", name)
	}
	return in.callValue(funVal(&closure{decl: fd}), args, token.NoPos)
}
