// L16: 花括号变量展开器（gitea vars 场景）：合法/非法名字、Unicode、未定义变量、
// 双花括号与孤立括号的错误定位；随机模板确定性与跨 GC 重放。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"
)

var (
	failed  int32
	gcSink  [][]byte
	live    []string
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
		_ = strings.Repeat("x", 256)
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

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}
	return true
}

// expand 单遍扫描：{name} 替换；非法名字原样保留；未定义变量/提前嵌套/
// 未闭合或孤立括号记录首个错误，同时继续尽量展开后续内容。
func expand(s string, vars map[string]string) (string, error) {
	var b strings.Builder
	var firstErr error
	setErr := func(format string, args ...any) {
		if firstErr == nil {
			firstErr = fmt.Errorf(format, args...)
		}
	}
	for i := 0; i < len(s); {
		switch s[i] {
		case '{':
			if i+1 < len(s) && s[i+1] == '{' {
				rel := strings.IndexByte(s[i+2:], '}')
				if rel < 0 {
					setErr("unclosed %q at byte %d", "{{", i)
					b.WriteString(s[i:])
					i = len(s)
					continue
				}
				j := i + 2 + rel
				b.WriteString(s[i : j+1])
				setErr("nested brace at byte %d", i)
				i = j + 1
				continue
			}
			rel := strings.IndexByte(s[i+1:], '}')
			if rel < 0 {
				setErr("unclosed %q at byte %d", "{", i)
				b.WriteString(s[i:])
				i = len(s)
				continue
			}
			j := i + 1 + rel
			name := s[i+1 : j]
			if validName(name) {
				if v, ok := vars[name]; ok {
					b.WriteString(v)
				} else {
					b.WriteString(s[i : j+1])
					setErr("undefined variable %q at byte %d", name, i)
				}
			} else {
				b.WriteString(s[i : j+1])
			}
			i = j + 1
		case '}':
			setErr("unmatched %q at byte %d", "}", i)
			b.WriteByte('}')
			i++
		default:
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), firstErr
}

func main() {
	uafSeed = envSeed()
	vars := map[string]string{"a": "1", "b": "2", "c": "3", "一": "11", "二": "22", "三": "33"}

	// 1) gitea vars 测试中的固定场景。
	type fixed struct {
		in   string
		want string
		err  string
	}
	cases := []fixed{
		{"{a}", "1", ""},
		{"expand {a}, {b} and {c}, with non-var { } {#}", "expand 1, 2 and 3, with non-var { } {#}", ""},
		{"中文内容 {一}, {二} 和 {三} 中文结尾", "中文内容 11, 22 和 33 中文结尾", ""},
		{"expand {{a}, {b} and {c}", "expand {{a}, 2 and 3", ""},
		{"expand } {} and {", "expand } {} and {", ""},
		{"prefix {a}{b}{一} suffix", "prefix 1211 suffix", ""},
		{"{missing}", "{missing}", "undefined variable"},
		{"{{a}", "{{a}", ""},
		{"a {b c} d", "a {b c} d", ""},
		{"{}", "{}", ""},
		{"{a", "{a", "unclosed"},
	}
	// 第 4/5 条带错误：单独断言错误子串。
	for idx, c := range cases {
		var wantErr string
		if idx == 3 {
			wantErr = "nested brace"
		} else if idx == 4 {
			wantErr = "unmatched"
		} else if idx == 6 {
			wantErr = "undefined variable"
		} else if idx == 7 {
			wantErr = "nested brace"
		} else if idx == 10 {
			wantErr = "unclosed"
		}
		got, err := expand(c.in, vars)
		ck(got == c.want, fmt.Sprintf("case %d output=%q want %q", idx, got, c.want))
		if wantErr == "" {
			ck(err == nil, fmt.Sprintf("case %d unexpected err=%v", idx, err))
		} else {
			ck(err != nil && strings.Contains(err.Error(), wantErr),
				fmt.Sprintf("case %d err=%v want %q", idx, err, wantErr))
		}
		live = append(live, got)
	}

	// 2) 错误位置稳定：同一输入重复展开错误文本一致。
	bad := "x {missing} y {{z} }"
	_, e1 := expand(bad, vars)
	_, e2 := expand(bad, vars)
	ck(e1 != nil && e2 != nil && e1.Error() == e2.Error(), fmt.Sprintf("error text unstable: %v vs %v", e1, e2))

	// 3) 20000 个占位符的批量展开。
	var big strings.Builder
	for i := 0; i < 20000; i++ {
		big.WriteString("{a}")
	}
	gotBig, errBig := expand(big.String(), vars)
	ck(errBig == nil && len(gotBig) == 20000, fmt.Sprintf("bulk len=%d err=%v", len(gotBig), errBig))
	ck(strings.Count(gotBig, "1") == 20000, "bulk substitution count")

	// 4) 种子驱动的随机模板：输出确定，两次结果一致，且不 panic。
	rng := rand.New(rand.NewSource(uafSeed))
	tokens := []string{"{", "}", "{}", "{a}", "{b}", "{一}", "{#}", "x", " ", "{{a}", "{missing}", "{ }", "_", "中"}
	for round := 0; round < 3; round++ {
		for n := 0; n < 300; n++ {
			var sb strings.Builder
			k := 1 + rng.Intn(10)
			for j := 0; j < k; j++ {
				sb.WriteString(tokens[rng.Intn(len(tokens))])
			}
			in := sb.String()
			out1, err1 := expand(in, vars)
			out2, err2 := expand(in, vars)
			ck(out1 == out2, fmt.Sprintf("round %d random output nondeterministic for %q", round, in))
			ck((err1 == nil) == (err2 == nil), fmt.Sprintf("round %d random error nondeterministic for %q", round, in))
			if err1 != nil {
				ck(err1.Error() == err2.Error(), fmt.Sprintf("round %d random error text for %q", round, in))
			}
			live = append(live, out1)
		}
		gcStress()
	}

	// 5) 参考实现：只含合法且有定义的占位符时，结果等于普通字符串替换。
	parts := []string{"lit", " ", "-", "{a}", "{b}", "{一}"}
	values := map[string]string{"{a}": "1", "{b}": "2", "{一}": "11"}
	for round := 0; round < 3; round++ {
		for n := 0; n < 100; n++ {
			var in, want strings.Builder
			k := 1 + rng.Intn(12)
			for j := 0; j < k; j++ {
				p := parts[rng.Intn(len(parts))]
				in.WriteString(p)
				if v, ok := values[p]; ok {
					want.WriteString(v)
				} else {
					want.WriteString(p)
				}
			}
			got, err := expand(in.String(), vars)
			ck(err == nil, fmt.Sprintf("reference case err=%v for %q", err, in.String()))
			ck(got == want.String(), fmt.Sprintf("reference mismatch for %q: %q vs %q", in.String(), got, want.String()))
		}
	}

	// 6) 输出值中的合法占位符再展开（值本身含花括号时的二次行为）稳定。
	vv := map[string]string{"a": "{b}", "b": "2"}
	o1, e1 := expand("{a}", vv)
	o2, e2 := expand(o1, vv)
	ck(e1 == nil && o1 == "{b}", fmt.Sprintf("first pass=%q err=%v", o1, e1))
	ck(e2 == nil && o2 == "2", fmt.Sprintf("second pass=%q err=%v", o2, e2))

	// 7) GC 后重放保留结果与固定用例。
	for round := 0; round < 3; round++ {
		gcStress()
		for idx, c := range cases {
			got, err := expand(c.in, vars)
			ck(got == c.want, fmt.Sprintf("round %d case %d output changed", round, idx))
			if idx == 3 || idx == 4 || idx == 6 || idx == 7 || idx == 10 {
				ck(err != nil, fmt.Sprintf("round %d case %d lost error", round, idx))
			}
		}
		ck(len(live) > 0 && live[0] == "1", fmt.Sprintf("round %d retained output lost", round))
		ck(vars["一"] == "11", fmt.Sprintf("round %d vars changed", round))
	}

	finish("l16_vars_expand")
}
