// L11: 字符串拼接链逃逸——多操作数 + 链与 Builder 结果存入 any/struct/
// map 键与切片后经历显式 GC；Builder 缓冲区复用不得改变已取出的字符串，
// 子串拼接与非法 UTF-8 字节在拼接后保持字节精确。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type seBox struct {
	s    string
	tag  string
	anyv any
}

type seRec struct {
	got  string
	want string
}

var (
	failed  int32
	gcSink  [][]byte
	seed    uint64 = 1
	seAny   []any
	seBoxes []*seBox
	seRecs  []seRec
	seKeys  []string
	seMap   map[string]int
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func tortureMultiplier() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	if n > 100 {
		n = 100
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}
	seMap = make(map[string]int, 64)
	rounds := 4 * tortureMultiplier()

	for r := 0; r < rounds; r++ {
		n := 120 + int((seed+uint64(r)*7)%80)
		parts := make([]string, n)
		totalLen := 0
		for i := range parts {
			switch i % 5 {
			case 0:
				parts[i] = strconv.Itoa((i*13 + r) % 1000)
			case 1:
				parts[i] = "世界"
			case 2:
				parts[i] = string([]byte{0xc3, byte(0xa9 + i%3)})
			case 3:
				parts[i] = "-\x00-"
			default:
				parts[i] = "xyz"
			}
			totalLen += len(parts[i])
		}

		var plus string
		for i := range parts {
			plus += parts[i]
		}
		var b strings.Builder
		b.Grow(totalLen)
		for _, p := range parts {
			b.WriteString(p)
		}
		bld := b.String()
		ck(plus == bld, "concat chain vs builder")
		ck(len(plus) == totalLen, "concat chain byte length")

		s1 := b.String()
		b.WriteString("TAIL")
		s2 := b.String()
		ck(s1 == plus, "builder string stable after append")
		ck(s2 == plus+"TAIL", "builder append result")
		ck(len(s1) == totalLen && len(s2) == totalLen+4, "builder string lengths after append")
		b.Reset()
		b.WriteString("reset")
		b.Reset()
		b.WriteString("reset2")
		ck(b.String() == "reset2", "builder reset reuse")
		ck(s1 == plus && s2 == plus+"TAIL", "builder reset preserves old strings")

		var combo string
		if len(plus) >= 16 {
			sub := plus[5:13]
			combo = sub + "|" + plus[:4] + "|" + plus[len(plus)-3:]
			ck(len(combo) == len(sub)+1+4+1+3, "substring chain length")
			ck(strings.HasPrefix(combo, sub+"|"), "substring chain prefix")
			ck(strings.HasSuffix(combo, "|"+plus[len(plus)-3:]), "substring chain suffix")
		} else {
			ck(false, "base string too short")
		}

		raw := string([]byte{0xff, 0xfe, 0x00}) + parts[r%n] + string([]byte{0x80, 0x81})
		wantRaw := "\xff\xfe\x00" + parts[r%n] + "\x80\x81"
		ck(raw == wantRaw, "invalid utf8 concatenation bytes")
		ck(len(raw) == 3+len(parts[r%n])+2, "invalid utf8 concatenation length")

		key := strconv.Itoa(r) + "\x00" + parts[0] + "\x01" + parts[2]
		seMap[key] = r
		seKeys = append(seKeys, key)

		seAny = append(seAny, plus, s1, combo, raw)
		seBoxes = append(seBoxes, &seBox{s: plus, tag: strconv.Itoa(r), anyv: s1})
		seRecs = append(seRecs, seRec{got: plus, want: bld}, seRec{got: s2, want: plus + "TAIL"})

		chained := parts[0] + "-" + parts[1] + "-" + parts[2] + "-" + parts[3] + "-" + parts[4]
		wantChain := parts[0] + "-" + parts[1] + "-" + parts[2] + "-" + parts[3] + "-" + parts[4]
		ck(chained == wantChain, "five operand chain")

		gcStress()
		v, ok := seBoxes[len(seBoxes)-1].anyv.(string)
		ck(ok && v == bld, "interface escaped builder string after GC")
		ck(seBoxes[len(seBoxes)-1].s == bld, "struct escaped concat after GC")
		if got, ok := seMap[key]; !ok || got != r {
			ck(false, "map key built by concat after GC")
		}
	}

	gcStress()

	recOK := true
	for _, rec := range seRecs {
		if rec.got != rec.want {
			recOK = false
			break
		}
	}
	ck(recOK, "retained concat results after GC")

	boxOK := true
	for _, box := range seBoxes {
		v, ok := box.anyv.(string)
		if !ok || v != box.s || box.tag == "" || box.s == "" {
			boxOK = false
			break
		}
	}
	ck(boxOK, "retained concat boxes after GC")

	anyOK := true
	for _, v := range seAny {
		s, ok := v.(string)
		if !ok || len(s) == 0 {
			anyOK = false
			break
		}
	}
	ck(anyOK, "retained interface strings after GC")

	keyOK := true
	for _, k := range seKeys {
		if _, ok := seMap[k]; !ok {
			keyOK = false
			break
		}
	}
	ck(keyOK, "retained concat map keys after GC")

	finish("l11_string_concat_escape")
}
