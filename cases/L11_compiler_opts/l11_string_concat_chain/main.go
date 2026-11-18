// L11: 字符串拼接链——2/3/5 操作数 +、常量折叠、循环 += 与 Builder/Join
// 等价、子串拼接独立、求值顺序与 GC 后内容稳定。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

var (
	failed  int32
	gcSink  [][]byte
	seed    uint64 = 1
	scTrace []int
	scKeep  []string
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

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func scPart(id int, s string) string {
	scTrace = append(scTrace, id)
	return s
}

func scA(i int64) string { return scPart(1, strconv.FormatInt(i, 10)) }
func scB(i int64) string { return scPart(2, "-b") }
func scC(i int64) string { return scPart(3, "-c") }
func scD(i int64) string { return scPart(4, "-d") }
func scE(i int64) string { return scPart(5, "-e") }

const scConst = "ab" + "cd" + "ef"

func main() {
	seed = loadSeed()
	if seed == 0 {
		seed = 1
	}

	scTrace = scTrace[:0]
	chain := scA(7) + scB(7) + scC(7) + scD(7) + scE(7)
	ck(chain == "7-b-c-d-e", "five operand concatenation result")
	ck(len(scTrace) == 5 && scTrace[0] == 1 && scTrace[1] == 2 && scTrace[2] == 3 && scTrace[3] == 4 && scTrace[4] == 5, "concatenation evaluation order")

	s := "hello"
	ck(""+s == s && s+"" == s, "empty operand concatenation")
	ck(""+"" == "", "empty concatenation")
	ck("a"+""+"b" == "a"+"b", "middle empty operand")
	ck("ab"+"cd" == "abcd" && len("ab"+"cd") == 4, "two operand concatenation")

	runtimeConst := "ab"
	runtimeConst += "cd"
	runtimeConst += "ef"
	ck(len(scConst) == 6 && scConst == "abcdef", "constant concatenation folded")
	ck(runtimeConst == scConst, "constant vs runtime concatenation")

	n := 256 + int(seed%128)
	parts := make([]string, n)
	for i := range parts {
		switch i % 4 {
		case 0:
			parts[i] = strconv.Itoa(i % 10)
		case 1:
			parts[i] = "-" + strconv.Itoa(i%7)
		case 2:
			parts[i] = "世界"
		default:
			parts[i] = string(rune('a'+i%26)) + ":"
		}
	}
	var plus string
	for _, p := range parts {
		plus += p
	}
	var builder strings.Builder
	totalLen := 0
	totalRunes := 0
	for _, p := range parts {
		totalLen += len(p)
		totalRunes += utf8.RuneCountInString(p)
	}
	builder.Grow(totalLen)
	for _, p := range parts {
		builder.WriteString(p)
	}
	joined := strings.Join(parts, "")
	ck(plus == builder.String(), "loop plus vs Builder")
	ck(plus == joined, "loop plus vs Join")
	ck(len(plus) == totalLen, "concatenated byte length")
	ck(utf8.RuneCountInString(plus) == totalRunes, "concatenated rune count")
	ck(strings.Count(plus, "世界") == (n+1)/4, "unicode fragment count")

	base := "0123456789abcdef"
	sub := base[3:9]
	ck(sub+"!" == "345678!", "substring concatenation")
	ck(sub+base[:1] == "3456780", "multiple substring concatenation")
	ck(sub+"" == sub && ""+sub == sub, "substring empty operands")
	ck(base == "0123456789abcdef", "base string unchanged")

	runeOK := true
	for i := 0; i < 10; i++ {
		want := string(rune('A'+i)) + strconv.Itoa(i)
		got := string(byte('A'+i)) + strconv.Itoa(i)
		if got != want {
			runeOK = false
			break
		}
	}
	ck(runeOK, "rune and byte conversion concatenation")

	scKeep = append(scKeep, chain, plus, joined, runtimeConst, sub+"!")
	gcStress()
	ck(scKeep[0] == "7-b-c-d-e" && len(scKeep[0]) == 9, "chain after GC")
	ck(len(scKeep[1]) == totalLen && scKeep[1] == scKeep[2], "long concatenation after GC")
	ck(scKeep[3] == "abcdef", "constant concatenation after GC")
	ck(scKeep[4] == "345678!", "substring concatenation after GC")
	ck(strings.Count(scKeep[1], "世界") == (n+1)/4, "unicode after GC")

	finish("l11_string_concat_chain")
}
