// L10: stringer 生成形态：偏移表 + 名称常量切片、前缀剥离、越界回退、
// MarshalText/UnmarshalText/GoString、稀疏枚举 switch；GC 后大字符串常量与子串仍有效。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// ---- Color：生成器剥离常量前缀 Color，String 走 name 常量 + 偏移表 ----

type Color int

const (
	ColorUnknown Color = iota
	ColorRed
	ColorDarkBlue
	ColorBrightGreen
	ColorNeonPink
	ColorDeepPurple
	colorCount
)

var _Color_index = [...]uint16{0, 7, 10, 18, 29, 37, 47}

const _Color_name = "UnknownRedDarkBlueBrightGreenNeonPinkDeepPurple"

func (i Color) String() string {
	if i < 0 || i >= colorCount {
		return "Color(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Color_name[_Color_index[i]:_Color_index[i+1]]
}

func (i Color) GoString() string {
	return "Color(" + strconv.FormatInt(int64(i), 10) + ")"
}

func (i Color) MarshalText() ([]byte, error) {
	if i < 0 || i >= colorCount {
		return nil, fmt.Errorf("Color: invalid value %d", int(i))
	}
	return []byte(i.String()), nil
}

func (i *Color) UnmarshalText(b []byte) error {
	if v, ok := colorByName[string(b)]; ok {
		*i = v
		return nil
	}
	return fmt.Errorf("Color: unknown value %q", string(b))
}

var colorByName map[string]Color

// ---- Shape：另一份生成产物，验证偏移表可并存 ----

type Shape int

const (
	ShapeCircle Shape = iota
	ShapeSquare
	ShapeRectangle
	ShapeTriangle
	shapeCount
)

var _Shape_index = [...]uint16{0, 6, 12, 21, 29}

const _Shape_name = "CircleSquareRectangleTriangle"

func (i Shape) String() string {
	if i < 0 || i >= shapeCount {
		return "Shape(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Shape_name[_Shape_index[i]:_Shape_index[i+1]]
}

// ---- Level：稀疏值域，stringer 退化为 switch ----

type Level int32

const (
	LevelNone Level = -100
	LevelLow  Level = 10
	LevelMid  Level = 20
	LevelHigh Level = 300
)

func (l Level) String() string {
	switch l {
	case LevelNone:
		return "None"
	case LevelLow:
		return "Low"
	case LevelMid:
		return "Mid"
	case LevelHigh:
		return "High"
	default:
		return "Level(" + strconv.FormatInt(int64(l), 10) + ")"
	}
}

var (
	colorWant = [...]string{"Unknown", "Red", "DarkBlue", "BrightGreen", "NeonPink", "DeepPurple"}
	shapeWant = [...]string{"Circle", "Square", "Rectangle", "Triangle"}
)

func blobHash(s string) uint64 {
	h := uint64(1469598103934665603)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func init() {
	colorByName = make(map[string]Color, len(colorWant))
	for i := Color(0); i < colorCount; i++ {
		colorByName[i.String()] = i
	}
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 偏移表/名称常量结构完整。
	ck(int(colorCount) == len(colorWant), "Color count")
	ck(len(_Color_index) == len(colorWant)+1, "Color index size")
	ck(int(_Color_index[len(_Color_index)-1]) == len(_Color_name), "Color index ends at name length")
	ck(int(shapeCount) == len(shapeWant), "Shape count")
	ck(int(_Shape_index[len(_Shape_index)-1]) == len(_Shape_name), "Shape index ends at name length")
	off := 0
	for i := 0; i < len(colorWant); i++ {
		if int(_Color_index[i]) != off {
			ck(false, "Color offsets monotonic")
			break
		}
		off += len(colorWant[i])
	}
	ck(off == len(_Color_name), "Color offsets cover name blob")
	ck(!strings.ContainsRune(_Color_name, 0) && !strings.ContainsRune(_Shape_name, 0), "name blobs have no NUL")
	ck(len(colorByName) == len(colorWant), "reverse map size")

	// 2. 全量取值映射 + 越界回退。
	for i := 0; i < len(colorWant); i++ {
		c := Color(i)
		name := c.String()
		ck(name == colorWant[i], "Color string value")
		ck(_Color_name[_Color_index[i]:_Color_index[i+1]] == colorWant[i], "Color slice range")
		if v, ok := colorByName[name]; !ok || v != c {
			ck(false, "Color reverse lookup")
		}
		txt, err := c.MarshalText()
		ck(err == nil && string(txt) == name, "Color MarshalText")
		var back Color
		ck(back.UnmarshalText(txt) == nil && back == c, "Color UnmarshalText roundtrip")
	}
	ck(Color(-1).String() == "Color(-1)", "Color negative fallback")
	ck(Color(colorCount).String() == "Color(6)", "Color upper fallback")
	ck(Color(1000000).String() == "Color(1000000)", "Color large fallback")
	ck(Color(-1000000).GoString() == "Color(-1000000)", "Color GoString")
	ck(fmt.Sprintf("%v", ColorDarkBlue) == "DarkBlue", "fmt uses String")
	ck(fmt.Sprintf("%#v", ColorRed) == "Color(1)", "fmt uses GoString")
	badTxt, err := ColorDeepPurple.MarshalText()
	ck(err == nil && string(badTxt) == "DeepPurple", "MarshalText valid")
	if _, err := Color(colorCount).MarshalText(); err == nil {
		ck(false, "MarshalText rejects invalid value")
	}
	for _, s := range []string{"", "Red ", "red", "UnknownRed", "Color(1)", "LEVEL", "DeepPurple!"} {
		var c Color
		if err := c.UnmarshalText([]byte(s)); err == nil {
			ck(false, "UnmarshalText rejects malformed name")
		}
	}

	// 3. Shape 与稀疏 Level。
	for i := 0; i < len(shapeWant); i++ {
		ck(Shape(i).String() == shapeWant[i], "Shape string value")
	}
	ck(Shape(-1).String() == "Shape(-1)", "Shape fallback")
	ck(Shape(shapeCount).String() == "Shape(4)", "Shape upper fallback")
	levelCases := []struct {
		l Level
		s string
	}{
		{LevelNone, "None"}, {LevelLow, "Low"}, {LevelMid, "Mid"}, {LevelHigh, "High"},
		{Level(0), "Level(0)"}, {Level(21), "Level(21)"}, {Level(-2147483648), "Level(-2147483648)"},
		{Level(2147483647), "Level(2147483647)"},
	}
	for _, tc := range levelCases {
		ck(tc.l.String() == tc.s, "Level string value")
	}

	// 4. GC 后名称常量、子串与反向映射仍然有效。
	hColor := blobHash(_Color_name)
	hShape := blobHash(_Shape_name)
	retained := make([]string, 0, len(colorWant)+len(shapeWant))
	for i := 0; i < len(colorWant); i++ {
		retained = append(retained, Color(i).String())
	}
	for i := 0; i < len(shapeWant); i++ {
		retained = append(retained, Shape(i).String())
	}
	enumSink := make([]Color, 0, 64)
	for i := 0; i < 64; i++ {
		enumSink = append(enumSink, Color(i%len(colorWant)))
	}
	gcSink = append(gcSink, []byte(retained[0]), []byte(retained[len(retained)-1]))
	gcStress()
	ck(blobHash(_Color_name) == hColor, "Color name blob stable after GC")
	ck(blobHash(_Shape_name) == hShape, "Shape name blob stable after GC")
	for i, s := range retained {
		if i < len(colorWant) {
			if s != colorWant[i] || Color(i).String() != s {
				ck(false, "retained Color name after GC")
				break
			}
		} else if s != shapeWant[i-len(colorWant)] || Shape(i-len(colorWant)).String() != s {
			ck(false, "retained Shape name after GC")
			break
		}
	}
	for i := range enumSink {
		if enumSink[i].String() != colorWant[i%len(colorWant)] {
			ck(false, "retained enum value after GC")
			break
		}
	}

	// 5. UAF_SEED 随机探测（有效/越界混合）。
	rng := rand.New(rand.NewSource(curSeed))
	for i := 0; i < 3000; i++ {
		switch rng.Intn(3) {
		case 0:
			v := rng.Intn(len(colorWant)+6) - 3
			c := Color(v)
			want := "Color(" + strconv.Itoa(v) + ")"
			if v >= 0 && v < len(colorWant) {
				want = colorWant[v]
			}
			ckSeed(c.String() == want, "random Color probe")
			if v >= 0 && v < len(colorWant) {
				ckSeed(colorByName[c.String()] == c, "random Color reverse probe")
			}
		case 1:
			v := rng.Intn(len(shapeWant)+4) - 2
			s := Shape(v)
			want := "Shape(" + strconv.Itoa(v) + ")"
			if v >= 0 && v < len(shapeWant) {
				want = shapeWant[v]
			}
			ckSeed(s.String() == want, "random Shape probe")
		default:
			v := Level(rng.Intn(500) - 120)
			var want string
			switch v {
			case LevelNone:
				want = "None"
			case LevelLow:
				want = "Low"
			case LevelMid:
				want = "Mid"
			case LevelHigh:
				want = "High"
			default:
				want = "Level(" + strconv.Itoa(int(v)) + ")"
			}
			ckSeed(v.String() == want, "random Level probe")
		}
		if i%500 == 0 {
			gcStress()
			ckSeed(blobHash(_Color_name) == hColor, "name blob stable in random loop")
		}
	}

	finish("l10_enum_stringer")
}
