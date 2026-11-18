// L16: go/types 对泛型实例化的方法集：值/指针接收者、嵌入提升、约束接口与 Implements，跨 GC。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const src = `package p

type Number interface{ ~int | ~int64 }

type Box[T any] struct{ V T }

func (b Box[T]) Get() T   { return b.V }
func (b *Box[T]) Set(v T) { b.V = v }

type Wrap[T any] struct{ Box[T] }

type NumBox[T Number] struct{ V T }

type Adder[T Number] interface {
	Add(T) T
	Get() T
}

type MyInt int

func (m MyInt) Add(n MyInt) MyInt { return m + n }
func (m MyInt) Get() MyInt        { return m }
`

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
	for i := 0; i < 32; i++ {
		_ = make([]*types.Var, 128)
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

type checked struct {
	pkg                                         *types.Package
	box, wrap, numbox, adder, myint             *types.Named
	boxInt, boxInt2, wrapStr, adderMy, numInt64 types.Type
	msBoxV, msBoxP, msWrapV, msWrapP, msAdder   *types.MethodSet
}

func names(ms *types.MethodSet) []string {
	out := make([]string, 0, ms.Len())
	for i := 0; i < ms.Len(); i++ {
		out = append(out, ms.At(i).Obj().Name())
	}
	return out
}

func namesEqual(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func load() (*checked, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "generic.go", src, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	var firstErr error
	conf := types.Config{Error: func(e error) {
		if firstErr == nil {
			firstErr = e
		}
	}}
	pkg, err := conf.Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		return nil, err
	}
	if firstErr != nil {
		return nil, firstErr
	}
	c := &checked{pkg: pkg}
	lookupNamed := func(name string) (*types.Named, error) {
		obj := pkg.Scope().Lookup(name)
		if obj == nil {
			return nil, fmt.Errorf("scope missing %s", name)
		}
		n, ok := obj.Type().(*types.Named)
		if !ok {
			return nil, fmt.Errorf("%s is not named", name)
		}
		return n, nil
	}
	if c.box, err = lookupNamed("Box"); err != nil {
		return nil, err
	}
	if c.wrap, err = lookupNamed("Wrap"); err != nil {
		return nil, err
	}
	if c.numbox, err = lookupNamed("NumBox"); err != nil {
		return nil, err
	}
	if c.adder, err = lookupNamed("Adder"); err != nil {
		return nil, err
	}
	if c.myint, err = lookupNamed("MyInt"); err != nil {
		return nil, err
	}
	inst := func(t *types.Named, arg types.Type) (types.Type, error) {
		return types.Instantiate(nil, t, []types.Type{arg}, true)
	}
	if c.boxInt, err = inst(c.box, types.Typ[types.Int]); err != nil {
		return nil, fmt.Errorf("Box[int]: %w", err)
	}
	if c.boxInt2, err = inst(c.box, types.Typ[types.Int]); err != nil {
		return nil, fmt.Errorf("Box[int] again: %w", err)
	}
	if c.wrapStr, err = inst(c.wrap, types.Typ[types.String]); err != nil {
		return nil, fmt.Errorf("Wrap[string]: %w", err)
	}
	if c.adderMy, err = inst(c.adder, c.myint); err != nil {
		return nil, fmt.Errorf("Adder[MyInt]: %w", err)
	}
	if c.numInt64, err = inst(c.numbox, types.Typ[types.Int64]); err != nil {
		return nil, fmt.Errorf("NumBox[int64]: %w", err)
	}
	c.msBoxV = types.NewMethodSet(c.boxInt)
	c.msBoxP = types.NewMethodSet(types.NewPointer(c.boxInt))
	c.msWrapV = types.NewMethodSet(c.wrapStr)
	c.msWrapP = types.NewMethodSet(types.NewPointer(c.wrapStr))
	c.msAdder = types.NewMethodSet(c.adderMy)
	return c, nil
}

func verify(c *checked, label string) {
	ck(c != nil, label+": nil checked")
	if c == nil {
		return
	}
	tps := c.box.TypeParams()
	ck(tps.Len() == 1, fmt.Sprintf("%s: Box type params=%d", label, tps.Len()))
	if tps.Len() == 1 {
		ck(tps.At(0).Obj().Name() == "T", fmt.Sprintf("%s: type param name=%s", label, tps.At(0).Obj().Name()))
		ck(types.TypeString(tps.At(0).Constraint(), nil) == "any",
			fmt.Sprintf("%s: constraint=%s", label, types.TypeString(tps.At(0).Constraint(), nil)))
	}
	ck(types.Identical(c.boxInt, c.boxInt2), label+": two Box[int] instantiations not identical")
	ck(types.TypeString(c.boxInt, nil) == "p.Box[int]", fmt.Sprintf("%s: Box[int] type=%s", label, types.TypeString(c.boxInt, nil)))
	ck(types.TypeString(c.wrapStr, nil) == "p.Wrap[string]", fmt.Sprintf("%s: Wrap[string] type=%s", label, types.TypeString(c.wrapStr, nil)))
	ck(types.TypeString(c.adderMy, nil) == "p.Adder[p.MyInt]", fmt.Sprintf("%s: Adder[MyInt] type=%s", label, types.TypeString(c.adderMy, nil)))
	ck(types.TypeString(c.numInt64, nil) == "p.NumBox[int64]", fmt.Sprintf("%s: NumBox[int64] type=%s", label, types.TypeString(c.numInt64, nil)))

	expect := []struct {
		label string
		ms    *types.MethodSet
		want  []string
	}{
		{"Box[int] value", c.msBoxV, []string{"Get"}},
		{"Box[int] ptr", c.msBoxP, []string{"Get", "Set"}},
		{"Wrap[string] value", c.msWrapV, []string{"Get"}},
		{"Wrap[string] ptr", c.msWrapP, []string{"Get", "Set"}},
		{"Adder[MyInt]", c.msAdder, []string{"Add", "Get"}},
	}
	for _, e := range expect {
		got := names(e.ms)
		ck(namesEqual(got, e.want...), fmt.Sprintf("%s: %s method set=%v want %v", label, e.label, got, e.want))
	}
	ck(c.msBoxV.Lookup(c.pkg, "Set") == nil, label+": value Box[int] exposes Set")
	ck(c.msWrapV.Lookup(c.pkg, "Set") == nil, label+": value Wrap[string] exposes Set")

	getSel := c.msBoxV.Lookup(c.pkg, "Get")
	ck(getSel != nil, label+": Box[int].Get missing")
	if getSel != nil {
		sig, ok := getSel.Obj().Type().(*types.Signature)
		ck(ok && sig.Params().Len() == 0 && sig.Results().Len() == 1,
			fmt.Sprintf("%s: Get signature=%v", label, getSel.Obj().Type()))
		if ok {
			ck(sig.Results().At(0).Type().String() == "int", fmt.Sprintf("%s: Get result=%v", label, sig.Results()))
			ck(sig.Recv() != nil && sig.Recv().Type().String() == "p.Box[int]",
				fmt.Sprintf("%s: Get receiver=%v", label, sig.Recv()))
		}
	}
	setSel := c.msBoxP.Lookup(c.pkg, "Set")
	ck(setSel != nil, label+": *Box[int].Set missing")
	if setSel != nil {
		sig, ok := setSel.Obj().Type().(*types.Signature)
		ck(ok && sig.Params().Len() == 1 && sig.Results().Len() == 0,
			fmt.Sprintf("%s: Set signature=%v", label, setSel.Obj().Type()))
		if ok {
			ck(sig.Params().At(0).Type().String() == "int", fmt.Sprintf("%s: Set param=%v", label, sig.Params()))
			ck(sig.Recv() != nil && sig.Recv().Type().String() == "*p.Box[int]",
				fmt.Sprintf("%s: Set receiver=%v", label, sig.Recv()))
		}
	}

	obj, idx, _ := types.LookupFieldOrMethod(c.wrapStr, true, c.pkg, "Get")
	ck(obj != nil && obj.Name() == "Get", fmt.Sprintf("%s: promoted Wrap.Get lookup=%v", label, obj))
	ck(len(idx) == 2, fmt.Sprintf("%s: promoted Wrap.Get index=%v", label, idx))
	obj, idx, _ = types.LookupFieldOrMethod(c.wrapStr, true, c.pkg, "Set")
	ck(obj != nil && obj.Name() == "Set", fmt.Sprintf("%s: promoted Wrap.Set lookup=%v", label, obj))
	ck(len(idx) == 2, fmt.Sprintf("%s: promoted Wrap.Set index=%v", label, idx))

	iface, ok := c.adderMy.Underlying().(*types.Interface)
	ck(ok, label+": Adder[MyInt] underlying not interface")
	if ok {
		iface.Complete()
		ck(iface.NumMethods() == 2, fmt.Sprintf("%s: Adder[MyInt] methods=%d", label, iface.NumMethods()))
		ck(iface.Method(0).Name() == "Add" && iface.Method(1).Name() == "Get",
			fmt.Sprintf("%s: Adder[MyInt] methods=%s,%s", label, iface.Method(0).Name(), iface.Method(1).Name()))
		ck(types.Implements(c.myint, iface), label+": MyInt does not implement Adder[MyInt]")
		ck(types.Implements(types.NewPointer(c.myint), iface), label+": *MyInt does not implement Adder[MyInt]")
	}

	_, err := types.Instantiate(nil, c.numbox, []types.Type{types.Typ[types.String]}, true)
	ck(err != nil, label+": NumBox[string] unexpectedly instantiated")
	if err != nil {
		ck(contains(err.Error(), "does not satisfy p.Number"),
			fmt.Sprintf("%s: NumBox[string] error=%q", label, err.Error()))
	}

	boxMy, err := types.Instantiate(nil, c.box, []types.Type{c.myint}, true)
	ck(err == nil, fmt.Sprintf("%s: Box[MyInt] instantiate: %v", label, err))
	if err == nil {
		ms := types.NewMethodSet(boxMy)
		ck(namesEqual(names(ms), "Get"), fmt.Sprintf("%s: Box[MyInt] methods=%v", label, names(ms)))
		if sel := ms.Lookup(c.pkg, "Get"); sel != nil {
			sig := sel.Obj().Type().(*types.Signature)
			ck(sig.Results().At(0).Type().String() == "p.MyInt",
				fmt.Sprintf("%s: Box[MyInt].Get result=%v", label, sig.Results()))
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func main() {
	uafSeed = envSeed()
	rounds := 3 + int(uafSeed%3)
	var badErr string

	for r := 0; r < rounds; r++ {
		c, err := load()
		ck(err == nil, fmt.Sprintf("round %d load: %v", r, err))
		if err != nil {
			continue
		}
		live = []any{c, c.pkg, c.boxInt, c.boxInt2, c.wrapStr, c.adderMy, c.numInt64,
			c.msBoxV, c.msBoxP, c.msWrapV, c.msWrapP, c.msAdder}
		verify(c, fmt.Sprintf("round %d", r))

		_, berr := types.Instantiate(nil, c.numbox, []types.Type{types.Typ[types.String]}, true)
		ck(berr != nil, fmt.Sprintf("round %d NumBox[string] expected error", r))
		if berr != nil {
			if r == 0 {
				badErr = berr.Error()
			} else {
				ck(berr.Error() == badErr, fmt.Sprintf("round %d constraint error changed: %q", r, berr.Error()))
			}
		}

		gcStress()

		ck(types.Identical(c.boxInt, c.boxInt2), fmt.Sprintf("round %d instantiated types diverged after GC", r))
		verify(c, fmt.Sprintf("round %d after GC", r))
		ck(types.NewMethodSet(c.boxInt).Len() == 1, fmt.Sprintf("round %d Box[int] method count after GC", r))
		ck(types.NewMethodSet(types.NewPointer(c.boxInt)).Len() == 2,
			fmt.Sprintf("round %d *Box[int] method count after GC", r))
		_, berr = types.Instantiate(nil, c.numbox, []types.Type{types.Typ[types.String]}, true)
		ck(berr != nil && berr.Error() == badErr, fmt.Sprintf("round %d constraint error after GC=%v", r, berr))
	}

	finish("l16_types_generic_methodset")
}
