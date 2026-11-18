// L16: go/parser AST 上实现最小算术解释器（函数调用/递归/if/for/赋值），与编译执行结果交叉校验，跨 GC。
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

const src = `package mini

func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func accum(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		if i%3 == 0 {
			total = total + i
		} else {
			if i%3 == 1 {
				total = total + (i * 2)
			} else {
				total = total - 1
			}
		}
	}
	return total
}

func gcd(a int, b int) int {
	for b != 0 {
		t := b
		b = a % b
		a = t
	}
	return a
}

func mix(a int, b int) int {
	x := a * b
	y := x + (a - b)
	if y > x {
		return y
	}
	return x
}

func div(a int, b int) int {
	return a / b
}

func spin(n int) int {
	for n > 0 {
	}
	return n
}

func bad() int {
	return nope(1)
}

func undecl() int {
	return zzz
}
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
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	for i := 0; i < 32; i++ {
		_ = make([]ast.Stmt, 128)
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
	if n > 200000 {
		n = 200000
	}
	return n
}

// ---- 极简解释器 ----

type ctrl int

const (
	ctrlNone ctrl = iota
	ctrlReturn
	ctrlBreak
	ctrlContinue
)

type env struct {
	m map[string]int64
}

func newEnv() *env { return &env{m: make(map[string]int64)} }

func (e *env) get(name string) (int64, bool) {
	v, ok := e.m[name]
	return v, ok
}

func (e *env) set(name string, v int64) bool {
	if _, ok := e.m[name]; !ok {
		return false
	}
	e.m[name] = v
	return true
}

type interp struct {
	funcs    map[string]*ast.FuncDecl
	maxDepth int
	maxSteps int
	steps    int
	depth    int
}

func newInterp(f *ast.File) *interp {
	in := &interp{funcs: map[string]*ast.FuncDecl{}, maxDepth: 200, maxSteps: 500000}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Name != nil && fd.Recv == nil {
			in.funcs[fd.Name.Name] = fd
		}
	}
	return in
}

func (in *interp) call(name string, args []int64) (int64, error) {
	fd, ok := in.funcs[name]
	if !ok {
		return 0, fmt.Errorf("unknown function %q", name)
	}
	var params []*ast.Ident
	for _, field := range fd.Type.Params.List {
		for _, nm := range field.Names {
			params = append(params, nm)
		}
	}
	if len(args) != len(params) {
		return 0, fmt.Errorf("function %s expects %d args, got %d", name, len(params), len(args))
	}
	if in.depth >= in.maxDepth {
		return 0, fmt.Errorf("call depth exceeded at %s", name)
	}
	in.depth++
	defer func() { in.depth-- }()

	e := newEnv()
	for i, p := range params {
		e.m[p.Name] = args[i]
	}
	c, v, err := in.execBlock(e, fd.Body.List)
	if err != nil {
		return 0, err
	}
	if c != ctrlReturn {
		return 0, fmt.Errorf("function %s ended without return", name)
	}
	return v, nil
}

func (in *interp) execBlock(e *env, stmts []ast.Stmt) (ctrl, int64, error) {
	for _, st := range stmts {
		in.steps++
		if in.steps > in.maxSteps {
			return ctrlNone, 0, fmt.Errorf("step limit exceeded (%d)", in.maxSteps)
		}
		c, v, err := in.execStmt(e, st)
		if err != nil {
			return ctrlNone, 0, err
		}
		if c != ctrlNone {
			return c, v, nil
		}
	}
	return ctrlNone, 0, nil
}

func (in *interp) execStmt(e *env, st ast.Stmt) (ctrl, int64, error) {
	switch s := st.(type) {
	case *ast.ReturnStmt:
		if len(s.Results) != 1 {
			return ctrlNone, 0, fmt.Errorf("unsupported return with %d values", len(s.Results))
		}
		v, err := in.eval(e, s.Results[0])
		if err != nil {
			return ctrlNone, 0, err
		}
		return ctrlReturn, v, nil
	case *ast.AssignStmt:
		if len(s.Lhs) != len(s.Rhs) {
			return ctrlNone, 0, fmt.Errorf("unsupported assignment arity %d=%d", len(s.Lhs), len(s.Rhs))
		}
		vals := make([]int64, len(s.Rhs))
		for i, rhs := range s.Rhs {
			v, err := in.eval(e, rhs)
			if err != nil {
				return ctrlNone, 0, err
			}
			vals[i] = v
		}
		for i, lhs := range s.Lhs {
			id, ok := lhs.(*ast.Ident)
			if !ok {
				return ctrlNone, 0, fmt.Errorf("unsupported assignment target %T", lhs)
			}
			switch s.Tok {
			case token.DEFINE:
				e.m[id.Name] = vals[i]
			case token.ASSIGN:
				if !e.set(id.Name, vals[i]) {
					return ctrlNone, 0, fmt.Errorf("assignment to undefined %q", id.Name)
				}
			default:
				return ctrlNone, 0, fmt.Errorf("unsupported assignment operator %s", s.Tok)
			}
		}
		return ctrlNone, 0, nil
	case *ast.IncDecStmt:
		id, ok := s.X.(*ast.Ident)
		if !ok {
			return ctrlNone, 0, fmt.Errorf("unsupported inc/dec target %T", s.X)
		}
		v, ok := e.get(id.Name)
		if !ok {
			return ctrlNone, 0, fmt.Errorf("inc/dec of undefined %q", id.Name)
		}
		switch s.Tok {
		case token.INC:
			e.m[id.Name] = v + 1
		case token.DEC:
			e.m[id.Name] = v - 1
		default:
			return ctrlNone, 0, fmt.Errorf("unsupported inc/dec token %s", s.Tok)
		}
		return ctrlNone, 0, nil
	case *ast.IfStmt:
		if s.Init != nil {
			if _, _, err := in.execStmt(e, s.Init); err != nil {
				return ctrlNone, 0, err
			}
		}
		cond, err := in.eval(e, s.Cond)
		if err != nil {
			return ctrlNone, 0, err
		}
		if cond != 0 {
			return in.execBlock(e, s.Body.List)
		}
		if s.Else != nil {
			if blk, ok := s.Else.(*ast.BlockStmt); ok {
				return in.execBlock(e, blk.List)
			}
			return in.execStmt(e, s.Else)
		}
		return ctrlNone, 0, nil
	case *ast.ForStmt:
		if s.Init != nil {
			if _, _, err := in.execStmt(e, s.Init); err != nil {
				return ctrlNone, 0, err
			}
		}
		for {
			in.steps++
			if in.steps > in.maxSteps {
				return ctrlNone, 0, fmt.Errorf("step limit exceeded (%d)", in.maxSteps)
			}
			if s.Cond != nil {
				cond, err := in.eval(e, s.Cond)
				if err != nil {
					return ctrlNone, 0, err
				}
				if cond == 0 {
					break
				}
			}
			c, v, err := in.execBlock(e, s.Body.List)
			if err != nil {
				return ctrlNone, 0, err
			}
			if c == ctrlReturn {
				return c, v, nil
			}
			if c == ctrlBreak {
				break
			}
			if s.Post != nil {
				if _, _, err := in.execStmt(e, s.Post); err != nil {
					return ctrlNone, 0, err
				}
			}
		}
		return ctrlNone, 0, nil
	case *ast.BlockStmt:
		return in.execBlock(e, s.List)
	case *ast.BranchStmt:
		switch s.Tok {
		case token.BREAK:
			return ctrlBreak, 0, nil
		case token.CONTINUE:
			return ctrlContinue, 0, nil
		}
		return ctrlNone, 0, fmt.Errorf("unsupported branch %s", s.Tok)
	case *ast.ExprStmt:
		if _, err := in.eval(e, s.X); err != nil {
			return ctrlNone, 0, err
		}
		return ctrlNone, 0, nil
	}
	return ctrlNone, 0, fmt.Errorf("unsupported statement %T", st)
}

func (in *interp) eval(e *env, expr ast.Expr) (int64, error) {
	in.steps++
	if in.steps > in.maxSteps {
		return 0, fmt.Errorf("step limit exceeded (%d)", in.maxSteps)
	}
	switch x := expr.(type) {
	case *ast.BasicLit:
		if x.Kind != token.INT {
			return 0, fmt.Errorf("unsupported literal %s", x.Kind)
		}
		v, err := strconv.ParseInt(x.Value, 0, 64)
		if err != nil {
			return 0, fmt.Errorf("bad integer literal %q: %v", x.Value, err)
		}
		return v, nil
	case *ast.Ident:
		v, ok := e.get(x.Name)
		if !ok {
			return 0, fmt.Errorf("unknown identifier %q", x.Name)
		}
		return v, nil
	case *ast.ParenExpr:
		return in.eval(e, x.X)
	case *ast.UnaryExpr:
		v, err := in.eval(e, x.X)
		if err != nil {
			return 0, err
		}
		switch x.Op {
		case token.ADD:
			return v, nil
		case token.SUB:
			return -v, nil
		}
		return 0, fmt.Errorf("unsupported unary operator %s", x.Op)
	case *ast.BinaryExpr:
		l, err := in.eval(e, x.X)
		if err != nil {
			return 0, err
		}
		r, err := in.eval(e, x.Y)
		if err != nil {
			return 0, err
		}
		switch x.Op {
		case token.ADD:
			return l + r, nil
		case token.SUB:
			return l - r, nil
		case token.MUL:
			return l * r, nil
		case token.QUO:
			if r == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return l / r, nil
		case token.REM:
			if r == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return l % r, nil
		case token.EQL:
			return b2i(l == r), nil
		case token.NEQ:
			return b2i(l != r), nil
		case token.LSS:
			return b2i(l < r), nil
		case token.LEQ:
			return b2i(l <= r), nil
		case token.GTR:
			return b2i(l > r), nil
		case token.GEQ:
			return b2i(l >= r), nil
		}
		return 0, fmt.Errorf("unsupported binary operator %s", x.Op)
	case *ast.CallExpr:
		fn, ok := x.Fun.(*ast.Ident)
		if !ok {
			return 0, fmt.Errorf("unsupported callee %T", x.Fun)
		}
		args := make([]int64, len(x.Args))
		for i, a := range x.Args {
			v, err := in.eval(e, a)
			if err != nil {
				return 0, err
			}
			args[i] = v
		}
		return in.call(fn.Name, args)
	}
	return 0, fmt.Errorf("unsupported expression %T", expr)
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// ---- 编译执行的参考实现 ----

func refFib(n int64) int64 {
	if n < 2 {
		return n
	}
	return refFib(n-1) + refFib(n-2)
}

func refAccum(n int64) int64 {
	total := int64(0)
	for i := int64(0); i < n; i++ {
		switch {
		case i%3 == 0:
			total += i
		case i%3 == 1:
			total += i * 2
		default:
			total--
		}
	}
	return total
}

func refGcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func refMix(a, b int64) int64 {
	x := a * b
	y := x + (a - b)
	if y > x {
		return y
	}
	return x
}

func main() {
	uafSeed = envSeed()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "mini.go", src, parser.ParseComments|parser.AllErrors)
	ck(err == nil, fmt.Sprintf("parse: %v", err))
	if err != nil {
		finish("l16_mini_interp")
		return
	}
	liveAST = append(liveAST, f)
	in := newInterp(f)
	ck(len(in.funcs) == 8, fmt.Sprintf("funcs=%d want 8", len(in.funcs)))

	// 函数位置映射在 GC 前后一致。
	posLines := map[string]int{}
	for name, fd := range in.funcs {
		p := fset.Position(fd.Pos())
		ck(p.Line > 1, fmt.Sprintf("func %s line=%d", name, p.Line))
		posLines[name] = p.Line
	}

	rounds := tortureRounds(6)
	rng := rand.New(rand.NewSource(uafSeed))
	var results []string
	for r := 0; r < rounds; r++ {
		for n := int64(0); n <= 18; n++ {
			in.steps = 0
			got, err := in.call("fib", []int64{n})
			ck(err == nil, fmt.Sprintf("round %d fib(%d): %v", r, n, err))
			want := refFib(n)
			ck(got == want, fmt.Sprintf("round %d fib(%d)=%d want %d", r, n, got, want))
		}
		for i := 0; i < 8; i++ {
			n := rng.Int63n(41)
			in.steps = 0
			got, err := in.call("accum", []int64{n})
			ck(err == nil, fmt.Sprintf("round %d accum(%d): %v", r, n, err))
			ck(got == refAccum(n), fmt.Sprintf("round %d accum(%d)=%d want %d", r, n, got, refAccum(n)))

			a, b := rng.Int63n(1000), rng.Int63n(1000)
			in.steps = 0
			got, err = in.call("gcd", []int64{a, b})
			ck(err == nil, fmt.Sprintf("round %d gcd(%d,%d): %v", r, a, b, err))
			ck(got == refGcd(a, b), fmt.Sprintf("round %d gcd(%d,%d)=%d want %d", r, a, b, got, refGcd(a, b)))

			x, y := rng.Int63n(41)-20, rng.Int63n(41)-20
			in.steps = 0
			got, err = in.call("mix", []int64{x, y})
			ck(err == nil, fmt.Sprintf("round %d mix(%d,%d): %v", r, x, y, err))
			ck(got == refMix(x, y), fmt.Sprintf("round %d mix(%d,%d)=%d want %d", r, x, y, got, refMix(x, y)))
			results = append(results, fmt.Sprintf("%d/%d/%d/%d", n, a, b, got))
		}

		in.steps = 0
		got, err := in.call("div", []int64{21, 4})
		ck(err == nil && got == 5, fmt.Sprintf("round %d div(21,4)=(%d,%v)", r, got, err))
		in.steps = 0
		_, err = in.call("div", []int64{1, 0})
		ck(err != nil && strings.Contains(err.Error(), "division by zero"),
			fmt.Sprintf("round %d div by zero error=%v", r, err))
		in.steps = 0
		_, err = in.call("spin", []int64{3})
		ck(err != nil && strings.Contains(err.Error(), "step limit"),
			fmt.Sprintf("round %d spin error=%v", r, err))
		in.steps = 0
		_, err = in.call("bad", nil)
		ck(err != nil && strings.Contains(err.Error(), `unknown function "nope"`),
			fmt.Sprintf("round %d bad error=%v", r, err))
		in.steps = 0
		_, err = in.call("undecl", nil)
		ck(err != nil && strings.Contains(err.Error(), `unknown identifier "zzz"`),
			fmt.Sprintf("round %d undecl error=%v", r, err))
		in.steps = 0
		_, err = in.call("fib", []int64{1, 2})
		ck(err != nil && strings.Contains(err.Error(), "expects 1 args"),
			fmt.Sprintf("round %d arity error=%v", r, err))
		in.steps = 0
		_, err = in.call("nosuch", nil)
		ck(err != nil && strings.Contains(err.Error(), "unknown function"),
			fmt.Sprintf("round %d unknown function error=%v", r, err))

		gcStress()

		// GC 后 AST/函数表仍可解释执行，结果与存储值一致。
		ck(len(liveAST) == 1 && liveAST[0] == f, "AST lost after GC")
		for name, line := range posLines {
			fd := in.funcs[name]
			ck(fd != nil && fset.Position(fd.Pos()).Line == line,
				fmt.Sprintf("round %d func %s position changed after GC", r, name))
		}
		in.steps = 0
		got, err = in.call("fib", []int64{15})
		ck(err == nil && got == refFib(15), fmt.Sprintf("round %d fib(15) after GC=(%d,%v)", r, got, err))
		in.steps = 0
		got, err = in.call("accum", []int64{25})
		ck(err == nil && got == refAccum(25), fmt.Sprintf("round %d accum(25) after GC=(%d,%v)", r, got, err))
		for _, s := range results {
			parts := strings.Split(s, "/")
			if len(parts) != 4 {
				ck(false, fmt.Sprintf("round %d bad stored result %q", r, s))
				continue
			}
			n, _ := strconv.ParseInt(parts[0], 10, 64)
			in.steps = 0
			v, e := in.call("accum", []int64{n})
			ck(e == nil && v == refAccum(n), fmt.Sprintf("round %d replay accum(%d)=(%d,%v)", r, n, v, e))
		}
	}

	finish("l16_mini_interp")
}
