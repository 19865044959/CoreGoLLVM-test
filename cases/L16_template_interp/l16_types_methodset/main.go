// L16: go/types 方法集：值/指针接收者、嵌入提升、接口嵌入、Implements/MissingMethod/Lookup 与 GC。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const src = `package p

type Base struct{ N int }

func (b Base) Value() int { return b.N }
func (b *Base) Ptr() int  { return b.N + 1 }

type Mid struct{ Base }

func (m Mid) MidOnly() string { return "mid" }

type Outer struct{ *Mid }

func (o Outer) OuterOnly() string { return "outer" }

type IVal interface {
	Value() int
}

type IBoth interface {
	Value() int
	Ptr() int
}

type IOuter interface {
	IVal
	OuterOnly() string
}
`

var (
	failed  int32
	gcSink  [][]byte
	live    []*types.Named
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
	pkg    *types.Package
	named  map[string]*types.Named
	ifaces map[string]*types.Interface
	ms     map[string]*types.MethodSet
}

func methodNames(ms *types.MethodSet) []string {
	names := make([]string, 0, ms.Len())
	for i := 0; i < ms.Len(); i++ {
		names = append(names, ms.At(i).Obj().Name())
	}
	return names
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

func load(srcText string) (*checked, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "methods.go", srcText, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	var firstErr error
	conf := types.Config{Error: func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}}
	pkg, err := conf.Check("p", fset, []*ast.File{f}, nil)
	if err != nil {
		return nil, err
	}
	if firstErr != nil {
		return nil, firstErr
	}
	c := &checked{pkg: pkg, named: map[string]*types.Named{}, ifaces: map[string]*types.Interface{}, ms: map[string]*types.MethodSet{}}
	for _, name := range []string{"Base", "Mid", "Outer", "IVal", "IBoth", "IOuter"} {
		obj := pkg.Scope().Lookup(name)
		if obj == nil {
			return nil, fmt.Errorf("scope missing %s", name)
		}
		if iface, ok := obj.Type().Underlying().(*types.Interface); ok {
			c.ifaces[name] = iface
		}
		named, ok := obj.Type().(*types.Named)
		if !ok {
			return nil, fmt.Errorf("%s is not named", name)
		}
		c.named[name] = named
	}
	for _, key := range []string{"Base", "*Base", "Mid", "*Mid", "Outer", "*Outer", "IVal", "IBoth", "IOuter", "*IVal"} {
		switch key {
		case "Base", "Mid", "Outer", "IVal", "IBoth", "IOuter":
			c.ms[key] = types.NewMethodSet(c.named[key])
		case "*Base":
			c.ms[key] = types.NewMethodSet(types.NewPointer(c.named["Base"]))
		case "*Mid":
			c.ms[key] = types.NewMethodSet(types.NewPointer(c.named["Mid"]))
		case "*Outer":
			c.ms[key] = types.NewMethodSet(types.NewPointer(c.named["Outer"]))
		case "*IVal":
			c.ms[key] = types.NewMethodSet(types.NewPointer(c.named["IVal"]))
		}
	}
	return c, nil
}

func verify(c *checked, label string) {
	ck(c != nil, label+": nil result")
	if c == nil {
		return
	}
	expect := map[string][]string{
		"Base":   {"Value"},
		"*Base":  {"Ptr", "Value"},
		"Mid":    {"MidOnly", "Value"},
		"*Mid":   {"MidOnly", "Ptr", "Value"},
		"Outer":  {"MidOnly", "OuterOnly", "Ptr", "Value"},
		"*Outer": {"MidOnly", "OuterOnly", "Ptr", "Value"},
		"IVal":   {"Value"},
		"IBoth":  {"Ptr", "Value"},
		"IOuter": {"OuterOnly", "Value"},
		"*IVal":  {},
	}
	for name, want := range expect {
		ms := c.ms[name]
		got := methodNames(ms)
		ck(namesEqual(got, want...), fmt.Sprintf("%s: method set %s=%v want %v", label, name, got, want))
		for _, m := range want {
			ck(ms.Lookup(c.pkg, m) != nil, fmt.Sprintf("%s: %s method set missing %s", label, name, m))
		}
	}
	// 签名与接收者类型。
	valueSel := c.ms["Base"].Lookup(c.pkg, "Value")
	ck(valueSel != nil, label+": Value selection missing")
	if valueSel != nil {
		sig, ok := valueSel.Obj().Type().(*types.Signature)
		ck(ok && sig.Results().Len() == 1 && sig.Params().Len() == 0,
			fmt.Sprintf("%s: Value signature %v", label, valueSel.Obj().Type()))
		ck(sig != nil && sig.Recv() != nil && sig.Recv().Type().String() == "p.Base",
			fmt.Sprintf("%s: Value receiver=%v", label, sig.Recv()))
		ck(sig != nil && sig.Results().At(0).Type().String() == "int",
			fmt.Sprintf("%s: Value result=%v", label, sig.Results()))
	}
	ptrSel := c.ms["*Base"].Lookup(c.pkg, "Ptr")
	ck(ptrSel != nil, label+": Ptr selection missing")
	if ptrSel != nil {
		sig, ok := ptrSel.Obj().Type().(*types.Signature)
		ck(ok, label+": Ptr is not a signature")
		if ok {
			ck(sig.Recv().Type().String() == "*p.Base", fmt.Sprintf("%s: Ptr receiver=%v", label, sig.Recv()))
		}
	}

	// Implements / MissingMethod。
	impl := []struct {
		typ  *types.Named
		ptr  bool
		ifac *types.Interface
		want bool
	}{
		{c.named["Base"], false, c.ifaces["IVal"], true},
		{c.named["Base"], true, c.ifaces["IVal"], true},
		{c.named["Base"], false, c.ifaces["IBoth"], false},
		{c.named["Base"], true, c.ifaces["IBoth"], true},
		{c.named["Mid"], false, c.ifaces["IVal"], true},
		{c.named["Mid"], false, c.ifaces["IBoth"], false},
		{c.named["Mid"], true, c.ifaces["IBoth"], true},
		{c.named["Outer"], false, c.ifaces["IBoth"], true},
		{c.named["Outer"], true, c.ifaces["IBoth"], true},
		{c.named["Outer"], false, c.ifaces["IOuter"], true},
		{c.named["Mid"], false, c.ifaces["IOuter"], false},
		{c.named["Outer"], true, c.ifaces["IOuter"], true},
	}
	order := rand.New(rand.NewSource(uafSeed + int64(len(label)))).Perm(len(impl))
	for _, i := range order {
		ic := impl[i]
		var t types.Type = ic.typ
		if ic.ptr {
			t = types.NewPointer(ic.typ)
		}
		got := types.Implements(t, ic.ifac)
		ck(got == ic.want, fmt.Sprintf("%s: Implements(%s ptr=%v, %s)=%v want %v",
			label, ic.typ.Obj().Name(), ic.ptr, ic.ifac.String(), got, ic.want))
	}
	for _, static := range []bool{true, false} {
		miss, wrong := types.MissingMethod(c.named["Mid"], c.ifaces["IBoth"], static)
		if miss == nil || miss.Name() != "Ptr" || !wrong {
			ck(false, fmt.Sprintf("%s: MissingMethod(Mid, IBoth, static=%v)=(%v,%v) want (Ptr,true)",
				label, static, miss, wrong))
		}
	}
	if miss, wrong := types.MissingMethod(c.named["Outer"], c.ifaces["IOuter"], true); miss != nil || wrong {
		ck(false, fmt.Sprintf("%s: MissingMethod(Outer, IOuter)=(%v,%v) want (nil,false)", label, miss, wrong))
	}

	// LookupFieldOrMethod 提升路径。
	obj, idx, indirect := types.LookupFieldOrMethod(c.named["Outer"], true, c.pkg, "Ptr")
	ck(obj != nil, label+": promoted Ptr not found")
	if obj != nil {
		ck(obj.Name() == "Ptr", fmt.Sprintf("%s: promoted Ptr obj=%s", label, obj.Name()))
		ck(len(idx) >= 2, fmt.Sprintf("%s: promoted Ptr index=%v", label, idx))
	}
	ck(indirect, label+": promoted pointer method not indirect")
	if obj, _, _ := types.LookupFieldOrMethod(c.named["Outer"], true, c.pkg, "OuterOnly"); obj == nil || obj.Name() != "OuterOnly" {
		ck(false, fmt.Sprintf("%s: OuterOnly lookup=%v", label, obj))
	}
	// 值类型上查找指针接收者方法：可寻址时仍能查到，但不在值方法集中。
	objMid, idxMid, indirectMid := types.LookupFieldOrMethod(c.named["Mid"], true, c.pkg, "Ptr")
	if objMid == nil || objMid.Name() != "Ptr" || len(idxMid) != 2 || indirectMid {
		ck(false, fmt.Sprintf("%s: Lookup(Mid.Ptr)=(%v,%v,%v) want (Ptr,[0 1],false)",
			label, objMid, idxMid, indirectMid))
	}
	// 接口方法集查找。
	sel := c.ms["IOuter"].Lookup(c.pkg, "Value")
	ck(sel != nil && sel.Kind() == types.MethodVal, fmt.Sprintf("%s: IOuter Value selection=%v", label, sel))
}

func main() {
	uafSeed = envSeed()
	rounds := 3 + int(uafSeed%3)

	for r := 0; r < rounds; r++ {
		c, err := load(src)
		ck(err == nil, fmt.Sprintf("round %d load/type-check: %v", r, err))
		if err != nil {
			continue
		}
		for _, name := range []string{"Base", "Mid", "Outer"} {
			live = append(live, c.named[name])
		}
		verify(c, fmt.Sprintf("round %d", r))
		gcStress()
		// 方法集缓存与 Named 对象在 GC 后仍需可用。
		verify(c, fmt.Sprintf("round %d after GC", r))
	}

	finish("l16_types_methodset")
}
