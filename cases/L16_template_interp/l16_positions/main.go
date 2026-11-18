// L16: go/token 位置、行号、字节列与 //line 指令映射；FileSet/AST 跨 GC 稳定。
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

const srcA = `package p
// note
const Alpha = 1
var Beta = Alpha + 2
func Gamma(n int) int {
	return n + Alpha
}
var Delta = "中文标识"
`

const srcB = `package q

func Helper() string {
	return "ok"
}
`

const srcC = `package q
//line fake.go:100:7
var V = 1
`

var (
	failed  int32
	gcSink  [][]byte
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
		_ = make([]token.Pos, 256)
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

func findIdent(f *ast.File, name string) *ast.Ident {
	var found *ast.Ident
	ast.Inspect(f, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = id
			return false
		}
		return true
	})
	return found
}

func findStringLit(f *ast.File) *ast.BasicLit {
	var found *ast.BasicLit
	ast.Inspect(f, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			found = lit
			return false
		}
		return true
	})
	return found
}

func lineOffsets(src string) []int {
	offs := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' && i+1 < len(src) {
			offs = append(offs, i+1)
		}
	}
	return offs
}

// checkFileCommon 校验 LineCount/LineStart/Base 等文件级映射。
func checkFileCommon(fset *token.FileSet, name, src string, f *ast.File) *token.File {
	tf := fset.File(f.Pos())
	ck(tf != nil, name+": fset.File returned nil")
	if tf == nil {
		return nil
	}
	ck(tf.Name() == name, fmt.Sprintf("%s: token.File name=%q", name, tf.Name()))
	ck(tf.Size() == len(src), fmt.Sprintf("%s: size=%d want %d", name, tf.Size(), len(src)))
	offs := lineOffsets(src)
	ck(tf.LineCount() == len(offs), fmt.Sprintf("%s: LineCount=%d want %d", name, tf.LineCount(), len(offs)))
	for i := 0; i < tf.LineCount() && i < len(offs); i++ {
		ck(tf.Offset(tf.LineStart(i+1)) == offs[i], fmt.Sprintf("%s: LineStart(%d)=%d want %d",
			name, i+1, tf.Offset(tf.LineStart(i+1)), offs[i]))
	}
	basePos := token.Pos(tf.Base())
	p := fset.Position(basePos)
	ck(p.Filename == name && p.Line == 1 && p.Column == 1 && p.Offset == 0,
		fmt.Sprintf("%s: base position=%+v", name, p))
	ck(tf.Offset(f.End()) == len(strings.TrimRight(src, "\n")),
		fmt.Sprintf("%s: last decl end offset=%d want %d", name, tf.Offset(f.End()), len(strings.TrimRight(src, "\n"))))
	ck(fset.File(basePos) == tf, name+": fset.File(base) mismatch")
	return tf
}

func checkPos(fset *token.FileSet, tf *token.File, id *ast.Ident, fname, label string, line, col, off int) {
	if id == nil {
		ck(false, label+": ident not found")
		return
	}
	p := fset.Position(id.Pos())
	ck(p.Filename == fname, fmt.Sprintf("%s: filename=%q want %q", label, p.Filename, fname))
	ck(p.Line == line && p.Column == col && p.Offset == off,
		fmt.Sprintf("%s: pos=%d:%d offset=%d want %d:%d offset=%d",
			label, p.Line, p.Column, p.Offset, line, col, off))
	ck(int(id.Pos())-tf.Base() == off, fmt.Sprintf("%s: Pos()-Base=%d want %d",
		label, int(id.Pos())-tf.Base(), off))
	ck(tf.Offset(id.Pos()) == off, fmt.Sprintf("%s: File.Offset=%d want %d", label, tf.Offset(id.Pos()), off))
	pf := fset.PositionFor(id.Pos(), false)
	ck(pf.Line == p.Line && pf.Column == p.Column && pf.Offset == p.Offset,
		fmt.Sprintf("%s: PositionFor(false)=%+v Position=%+v", label, pf, p))
}

func main() {
	uafSeed = envSeed()
	files := []struct {
		name string
		src  string
	}{
		{"a.go", srcA},
		{"b.go", srcB},
		{"c.go", srcC},
	}
	rng := rand.New(rand.NewSource(uafSeed))
	rounds := 4
	var fset *token.FileSet
	var parsedA, parsedB, parsedC *ast.File
	for r := 0; r < rounds; r++ {
		fset = token.NewFileSet()
		parsed := make([]*ast.File, len(files))
		for _, idx := range rng.Perm(len(files)) {
			f, err := parser.ParseFile(fset, files[idx].name, files[idx].src, parser.ParseComments|parser.AllErrors)
			ck(err == nil, fmt.Sprintf("round %d parse %s: %v", r, files[idx].name, err))
			parsed[idx] = f
		}
		parsedA, parsedB, parsedC = parsed[0], parsed[1], parsed[2]
		gcStress()
	}
	ck(fset != nil && parsedA != nil && parsedB != nil && parsedC != nil, "parse round produced nil files")
	if parsedA == nil || parsedB == nil || parsedC == nil {
		finish("l16_positions")
		return
	}

	tfA := checkFileCommon(fset, "a.go", srcA, parsedA)
	tfB := checkFileCommon(fset, "b.go", srcB, parsedB)
	tfC := checkFileCommon(fset, "c.go", srcC, parsedC)
	ck(tfA != nil && tfB != nil && tfC != nil, "token.File missing")
	if tfA == nil || tfB == nil || tfC == nil {
		finish("l16_positions")
		return
	}
	ck(tfA.Base() != tfB.Base() && tfB.Base() != tfC.Base() && tfA.Base() != tfC.Base(),
		fmt.Sprintf("file bases collide: %d %d %d", tfA.Base(), tfB.Base(), tfC.Base()))

	alphaOff := strings.Index(srcA, "Alpha")
	ck(alphaOff == 24, fmt.Sprintf("Alpha offset=%d want 24", alphaOff))
	checkPos(fset, tfA, findIdent(parsedA, "Alpha"), "a.go", "Alpha", 3, 7, alphaOff)
	checkPos(fset, tfA, findIdent(parsedA, "Beta"), "a.go", "Beta", 4, 5, strings.Index(srcA, "Beta"))
	checkPos(fset, tfA, findIdent(parsedA, "Gamma"), "a.go", "Gamma", 5, 6, strings.Index(srcA, "Gamma"))

	deltaOff := strings.Index(srcA, `"中文标识"`)
	lit := findStringLit(parsedA)
	if lit == nil {
		ck(false, "Delta string literal not found")
	} else {
		p := fset.Position(lit.Pos())
		e := fset.Position(lit.End())
		ck(p.Line == 8 && p.Column == 13 && p.Offset == deltaOff,
			fmt.Sprintf("Delta lit pos=%d:%d offset=%d want 8:13 offset=%d", p.Line, p.Column, p.Offset, deltaOff))
		ck(len(lit.Value) == 14, fmt.Sprintf("Delta literal byte len=%d want 14", len(lit.Value)))
		ck(e.Column == 27, fmt.Sprintf("Delta lit end column=%d want 27 (byte-based)", e.Column))
	}

	gamma := findIdent(parsedA, "Gamma")
	ck(gamma != nil, "Gamma ident not found")
	var gammaEndLine int
	ast.Inspect(parsedA, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FuncDecl); ok && fd.Name.Name == "Gamma" {
			gammaEndLine = fset.Position(fd.End()).Line
		}
		return true
	})
	ck(gammaEndLine == 7, fmt.Sprintf("Gamma end line=%d want 7", gammaEndLine))

	checkPos(fset, tfB, findIdent(parsedB, "Helper"), "b.go", "Helper", 3, 6, strings.Index(srcB, "Helper"))

	v := findIdent(parsedC, "V")
	if v == nil {
		ck(false, "V ident not found")
	} else {
		raw := fset.PositionFor(v.Pos(), false)
		adj := fset.PositionFor(v.Pos(), true)
		ck(raw.Filename == "c.go" && raw.Line == 3 && raw.Column == 5,
			fmt.Sprintf("V raw pos=%+v", raw))
		ck(adj.Filename == "fake.go" && adj.Line == 100 && adj.Column == 11,
			fmt.Sprintf("V adjusted pos=%+v want fake.go:100:11", adj))
		ck(fset.Position(v.Pos()).Filename == "fake.go", "Position default is not adjusted")
	}

	gcStress()

	// GC 后所有位置映射与 AST 仍可读。
	memoA := findIdent(parsedA, "Beta")
	checkPos(fset, tfA, memoA, "a.go", "Beta-after-GC", 4, 5, strings.Index(srcA, "Beta"))
	lit2 := findStringLit(parsedA)
	ck(lit2 != nil && lit2.Value == `"中文标识"`, "string literal lost after GC")
	ck(findIdent(parsedB, "Helper") != nil, "b.go AST broken after GC")
	v2 := findIdent(parsedC, "V")
	ck(v2 != nil && fset.Position(v2.Pos()).Line == 100, "line directive broken after GC")
	ck(fset.File(parsedA.Pos()) == tfA, "FileSet broken after GC")
	offs := lineOffsets(srcA)
	for i := 0; i < tfA.LineCount() && i < len(offs); i++ {
		ck(tfA.Offset(tfA.LineStart(i+1)) == offs[i], fmt.Sprintf("after GC LineStart(%d)=%d want %d",
			i+1, tfA.Offset(tfA.LineStart(i+1)), offs[i]))
	}

	finish("l16_positions")
}
