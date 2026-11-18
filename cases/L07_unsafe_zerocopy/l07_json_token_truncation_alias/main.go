// L07: JSON token 别名的截断边界。手写扁平对象扫描器，未转义 token 用 unsafe.String
// 直接引用输入，转义 token 复制到独立缓冲；对所有前缀长度必须拒绝，完整输入才接受。
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unicode/utf8"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
	seed   int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 12; i++ {
			b := make([]byte, 8192+i*13)
			b[0] = byte(i)
			b[len(b)-1] = byte(245 - i)
			gcSink = append(gcSink[:0], b)
		}
		gcSink = gcSink[:0]
		runtime.GC()
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type jtKind byte

const (
	jtStr jtKind = iota
	jtNum
	jtBool
	jtNull
)

type jtoken struct {
	key       string
	kind      jtKind
	val       string
	off       int
	n         int
	copied    bool
	keyOff    int
	keyN      int
	keyCopied bool
}

type jparser struct {
	b []byte
	i int
}

func (p *jparser) ws() {
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *jparser) rawString() (start, end int, ok bool) {
	if p.i >= len(p.b) || p.b[p.i] != '"' {
		return 0, 0, false
	}
	p.i++
	start = p.i
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case '\\':
			if p.i+1 >= len(p.b) {
				return 0, 0, false
			}
			p.i += 2
		case '"':
			end = p.i
			p.i++
			return start, end, true
		default:
			p.i++
		}
	}
	return 0, 0, false
}

func hasEscapeB(b []byte) bool {
	for _, c := range b {
		if c == '\\' {
			return true
		}
	}
	return false
}

func decodeEsc(raw []byte) (string, bool) {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' {
			out = append(out, c)
			continue
		}
		i++
		if i >= len(raw) {
			return "", false
		}
		switch raw[i] {
		case '"', '\\', '/':
			out = append(out, raw[i])
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			if i+4 >= len(raw) {
				return "", false
			}
			var v rune
			for k := 1; k <= 4; k++ {
				d := raw[i+k]
				var x byte
				switch {
				case d >= '0' && d <= '9':
					x = d - '0'
				case d >= 'a' && d <= 'f':
					x = d - 'a' + 10
				case d >= 'A' && d <= 'F':
					x = d - 'A' + 10
				default:
					return "", false
				}
				v = v<<4 | rune(x)
			}
			if !utf8.ValidRune(v) {
				return "", false
			}
			out = utf8.AppendRune(out, v)
			i += 4
		default:
			return "", false
		}
	}
	return string(out), true
}

func (p *jparser) value() (jtoken, bool) {
	var t jtoken
	if p.i >= len(p.b) {
		return t, false
	}
	c := p.b[p.i]
	if c == '"' {
		start, end, ok := p.rawString()
		if !ok {
			return t, false
		}
		raw := p.b[start:end]
		t.kind = jtStr
		t.off, t.n = start, end-start
		if hasEscapeB(raw) {
			v, ok := decodeEsc(raw)
			if !ok {
				return t, false
			}
			t.val, t.copied = v, true
		} else if len(raw) > 0 {
			t.val = unsafe.String(unsafe.SliceData(raw), len(raw))
		}
		return t, true
	}
	if c == '-' || (c >= '0' && c <= '9') {
		start := p.i
		if c == '-' {
			p.i++
			if p.i >= len(p.b) {
				return t, false
			}
		}
		if p.b[p.i] < '0' || p.b[p.i] > '9' {
			return t, false
		}
		for p.i < len(p.b) && p.b[p.i] >= '0' && p.b[p.i] <= '9' {
			p.i++
		}
		if p.i < len(p.b) && p.b[p.i] == '.' {
			p.i++
			if p.i >= len(p.b) || p.b[p.i] < '0' || p.b[p.i] > '9' {
				return t, false
			}
			for p.i < len(p.b) && p.b[p.i] >= '0' && p.b[p.i] <= '9' {
				p.i++
			}
		}
		raw := p.b[start:p.i]
		t.kind = jtNum
		t.off, t.n = start, len(raw)
		t.val = unsafe.String(unsafe.SliceData(raw), len(raw))
		return t, true
	}
	if c == 't' && bytes.HasPrefix(p.b[p.i:], []byte("true")) {
		t.kind, t.val, t.off, t.n = jtBool, "true", p.i, 4
		p.i += 4
		return t, true
	}
	if c == 'f' && bytes.HasPrefix(p.b[p.i:], []byte("false")) {
		t.kind, t.val, t.off, t.n = jtBool, "false", p.i, 5
		p.i += 5
		return t, true
	}
	if c == 'n' && bytes.HasPrefix(p.b[p.i:], []byte("null")) {
		t.kind, t.val, t.off, t.n = jtNull, "null", p.i, 4
		p.i += 4
		return t, true
	}
	return t, false
}

func (p *jparser) object() ([]jtoken, bool) {
	var out []jtoken
	p.ws()
	if p.i >= len(p.b) || p.b[p.i] != '{' {
		return nil, false
	}
	p.i++
	p.ws()
	if p.i < len(p.b) && p.b[p.i] == '}' {
		p.i++
		return out, true
	}
	for {
		p.ws()
		ks, ke, ok := p.rawString()
		if !ok {
			return nil, false
		}
		keyRaw := p.b[ks:ke]
		keyCopied := false
		var key string
		if hasEscapeB(keyRaw) {
			key, ok = decodeEsc(keyRaw)
			if !ok {
				return nil, false
			}
			keyCopied = true
		} else if len(keyRaw) > 0 {
			key = unsafe.String(unsafe.SliceData(keyRaw), len(keyRaw))
		}
		p.ws()
		if p.i >= len(p.b) || p.b[p.i] != ':' {
			return nil, false
		}
		p.i++
		p.ws()
		t, ok := p.value()
		if !ok {
			return nil, false
		}
		t.key, t.keyOff, t.keyN, t.keyCopied = key, ks, ke-ks, keyCopied
		out = append(out, t)
		p.ws()
		if p.i < len(p.b) && p.b[p.i] == ',' {
			p.i++
			continue
		}
		if p.i < len(p.b) && p.b[p.i] == '}' {
			p.i++
			return out, true
		}
		return nil, false
	}
}

func parseDoc(b []byte) ([]jtoken, bool) {
	p := &jparser{b: b}
	toks, ok := p.object()
	if !ok {
		return nil, false
	}
	p.ws()
	if p.i != len(b) {
		return nil, false
	}
	return toks, true
}

func parseOne(b []byte) (jtoken, bool) {
	p := &jparser{b: b}
	p.ws()
	t, ok := p.value()
	if !ok {
		return t, false
	}
	p.ws()
	if p.i != len(b) {
		return t, false
	}
	return t, true
}

func aliases(buf []byte, off int, s string) bool {
	if len(s) == 0 || off < 0 || off+len(s) > len(buf) {
		return false
	}
	return unsafe.Pointer(unsafe.StringData(s)) == unsafe.Pointer(&buf[off])
}

func buildDoc(s int64) []byte {
	var b bytes.Buffer
	b.WriteString(`{"a":"x","b":"","c":"boundary-`)
	b.WriteString(strconv.FormatInt(s, 10))
	b.WriteString(`","d":"a\"b\\c","e":-12.5,"f":true,"g":null,"h":"pad-`)
	b.WriteString(strconv.FormatInt(s*3, 10))
	b.WriteString(`","k\u0032":"esc-key"}`)
	return b.Bytes()
}

type jexpect struct {
	key    string
	kind   jtKind
	val    string
	copied bool
}

func expectations(s int64) []jexpect {
	return []jexpect{
		{"a", jtStr, "x", false},
		{"b", jtStr, "", false},
		{"c", jtStr, "boundary-" + strconv.FormatInt(s, 10), false},
		{"d", jtStr, "a\"b\\c", true},
		{"e", jtNum, "-12.5", false},
		{"f", jtBool, "true", false},
		{"g", jtNull, "null", false},
		{"h", jtStr, "pad-" + strconv.FormatInt(s*3, 10), false},
		{"k2", jtStr, "esc-key", false},
	}
}

func main() {
	if env := os.Getenv("UAF_SEED"); env != "" {
		if v, err := strconv.ParseInt(env, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}

	// 1) 完整输入解析成功，所有更短前缀都必须拒绝且不得 panic。
	buf := buildDoc(seed)
	orig := append([]byte(nil), buf...)
	toks, ok := parseDoc(buf)
	ck(ok, "parse complete document")
	ck(bytes.Equal(buf, orig), "input immutable after parse")
	for cut := 0; cut < len(buf); cut++ {
		if _, ok := parseDoc(buf[:cut]); ok {
			ck(false, "truncated document accepted cut="+strconv.Itoa(cut))
		}
	}

	// 2) 独立 token 的前缀截断：只有完整 token 才成功。
	standalone := []struct {
		raw    string
		want   string
		copied bool
		sweep  bool
	}{
		{`"abc"`, "abc", false, true},
		{`""`, "", false, true},
		{`"a\"b\\c"`, "a\"b\\c", true, true},
		{`"tail"`, "tail", false, true},
	}
	for ci := range standalone {
		raw := []byte(standalone[ci].raw)
		t, ok := parseOne(raw)
		ck(ok, "standalone token parse ci="+strconv.Itoa(ci))
		ck(t.val == standalone[ci].want, "standalone token value ci="+strconv.Itoa(ci))
		ck(t.copied == standalone[ci].copied, "standalone token copied ci="+strconv.Itoa(ci))
		if standalone[ci].sweep {
			for cut := 0; cut < len(raw); cut++ {
				if _, ok := parseOne(raw[:cut]); ok {
					ck(false, "truncated token accepted ci="+strconv.Itoa(ci)+" cut="+strconv.Itoa(cut))
				}
			}
		}
	}
	for _, bad := range []string{"12.", "-", ".5", "1e5", "tru", "truex", "nul"} {
		if _, ok := parseOne([]byte(bad)); ok {
			ck(false, "malformed token accepted: "+bad)
		}
	}
	nt, ok := parseOne([]byte("12345"))
	ck(ok && nt.kind == jtNum && nt.val == "12345", "number token complete")
	nb := []byte("12345")
	nt, ok = parseOne(nb)
	ck(ok && aliases(nb, 0, nt.val), "number token aliases input")

	// 3) 对象 token 的别名/拷贝语义。
	exps := expectations(seed)
	ck(len(toks) == len(exps), "token count")
	for i := 0; i < len(exps) && i < len(toks); i++ {
		t, e := &toks[i], exps[i]
		ck(t.key == e.key, "key i="+strconv.Itoa(i)+" got="+t.key)
		ck(t.kind == e.kind, "kind i="+strconv.Itoa(i))
		ck(t.val == e.val, "value i="+strconv.Itoa(i)+" got="+t.val)
		ck(t.copied == e.copied, "copied flag i="+strconv.Itoa(i))
		if !t.keyCopied && t.keyN > 0 {
			ck(aliases(buf, t.keyOff, t.key), "key aliases input i="+strconv.Itoa(i))
		}
		if !t.copied && (t.kind == jtStr || t.kind == jtNum) && t.n > 0 {
			ck(aliases(buf, t.off, t.val), "value aliases input i="+strconv.Itoa(i))
		}
		if t.copied {
			ck(!aliases(buf, t.off, t.val), "escaped value not aliased i="+strconv.Itoa(i))
		}
	}
	ck(toks[len(toks)-1].keyCopied, "escaped key marked copied")
	ck(toks[1].n == 0 && !toks[1].copied, "empty value has no alias")

	// 4) 别名可见性：改输入立即反映；转义值隔离。
	t0 := &toks[0]
	oldA := string([]byte(t0.val))
	buf[t0.off] ^= 0xff
	ck(t0.val != oldA, "aliased token observes input write")
	buf[t0.off] ^= 0xff
	ck(t0.val == oldA, "aliased token restored")
	t3 := &toks[3]
	oldD := t3.val
	buf[t3.off] ^= 0xff
	ck(t3.val == oldD, "copied escaped token isolated")
	buf[t3.off] ^= 0xff
	ck(bytes.Equal(buf, orig), "input restored")

	// 5) 丢弃输入，仅保留 token 视图，跨 GC 校验内容。
	buf = nil
	orig = nil
	gcStress()
	for i, e := range exps {
		t := &toks[i]
		ck(t.key == e.key, "key after GC i="+strconv.Itoa(i))
		ck(t.kind == e.kind, "kind after GC i="+strconv.Itoa(i))
		ck(t.val == e.val, "value after GC i="+strconv.Itoa(i))
	}
	ck(len(toks) == len(exps), "token count after GC")

	finish("l07_json_token_truncation_alias")
}
