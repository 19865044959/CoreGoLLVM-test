// L07: JSON token 解码器的字节别名。手写扁平对象扫描器，未转义 string/number 的
// token 用 unsafe.String 直接引用输入；转义值解码到独立缓冲。输入不可变，视图跨 GC 存活。
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
			b := make([]byte, 8192+i*17)
			b[0] = byte(i)
			b[len(b)-1] = byte(250 - i)
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

type tokKind byte

const (
	tokStr tokKind = iota
	tokNum
	tokBool
	tokNull
)

type token struct {
	key       string
	kind      tokKind
	val       string
	off       int
	n         int
	copied    bool
	keyOff    int
	keyN      int
	keyCopied bool
}

type parser struct {
	b []byte
	i int
}

func (p *parser) ws() {
	for p.i < len(p.b) {
		switch p.b[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) peek() byte {
	if p.i >= len(p.b) {
		return 0
	}
	return p.b[p.i]
}

func (p *parser) eat(c byte) bool {
	if p.i < len(p.b) && p.b[p.i] == c {
		p.i++
		return true
	}
	return false
}

func (p *parser) rawString() (start, end int, ok bool) {
	if !p.eat('"') {
		return 0, 0, false
	}
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

func hasEscape(b []byte) bool {
	for _, c := range b {
		if c == '\\' {
			return true
		}
	}
	return false
}

func decodeEscaped(raw []byte) (string, bool) {
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

func (p *parser) parseValue() (token, bool) {
	var t token
	switch c := p.peek(); {
	case c == '"':
		start, end, ok := p.rawString()
		if !ok {
			return t, false
		}
		raw := p.b[start:end]
		t.kind = tokStr
		t.off, t.n = start, end-start
		if hasEscape(raw) {
			s, ok := decodeEscaped(raw)
			if !ok {
				return t, false
			}
			t.val, t.copied = s, true
		} else if len(raw) > 0 {
			t.val = unsafe.String(unsafe.SliceData(raw), len(raw))
		}
		return t, true
	case c == '-' || (c >= '0' && c <= '9'):
		start := p.i
		if p.eat('-') && (p.i >= len(p.b) || p.b[p.i] < '0' || p.b[p.i] > '9') {
			return t, false
		}
		for p.i < len(p.b) && p.b[p.i] >= '0' && p.b[p.i] <= '9' {
			p.i++
		}
		if p.eat('.') {
			if p.i >= len(p.b) || p.b[p.i] < '0' || p.b[p.i] > '9' {
				return t, false
			}
			for p.i < len(p.b) && p.b[p.i] >= '0' && p.b[p.i] <= '9' {
				p.i++
			}
		}
		raw := p.b[start:p.i]
		t.kind = tokNum
		t.off, t.n = start, len(raw)
		t.val = unsafe.String(unsafe.SliceData(raw), len(raw))
		return t, true
	case c == 't':
		if bytes.HasPrefix(p.b[p.i:], []byte("true")) {
			t.kind, t.val, t.off, t.n = tokBool, "true", p.i, 4
			p.i += 4
			return t, true
		}
	case c == 'f':
		if bytes.HasPrefix(p.b[p.i:], []byte("false")) {
			t.kind, t.val, t.off, t.n = tokBool, "false", p.i, 5
			p.i += 5
			return t, true
		}
	case c == 'n':
		if bytes.HasPrefix(p.b[p.i:], []byte("null")) {
			t.kind, t.val, t.off, t.n = tokNull, "null", p.i, 4
			p.i += 4
			return t, true
		}
	}
	return t, false
}

func (p *parser) parseObject() ([]token, bool) {
	var out []token
	p.ws()
	if !p.eat('{') {
		return nil, false
	}
	p.ws()
	if p.eat('}') {
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
		if hasEscape(keyRaw) {
			key, ok = decodeEscaped(keyRaw)
			if !ok {
				return nil, false
			}
			keyCopied = true
		} else if len(keyRaw) > 0 {
			key = unsafe.String(unsafe.SliceData(keyRaw), len(keyRaw))
		}
		p.ws()
		if !p.eat(':') {
			return nil, false
		}
		p.ws()
		t, ok := p.parseValue()
		if !ok {
			return nil, false
		}
		t.key, t.keyOff, t.keyN, t.keyCopied = key, ks, ke-ks, keyCopied
		out = append(out, t)
		p.ws()
		if p.eat(',') {
			continue
		}
		if p.eat('}') {
			break
		}
		return nil, false
	}
	return out, true
}

func aliases(buf []byte, off int, s string) bool {
	if len(s) == 0 || off < 0 || off+len(s) > len(buf) {
		return false
	}
	return unsafe.Pointer(unsafe.StringData(s)) == unsafe.Pointer(&buf[off])
}

type expect struct {
	key    string
	kind   tokKind
	val    string
	copied bool
}

func buildJSON(s int64) []byte {
	var b bytes.Buffer
	b.WriteByte('{')
	fmt.Fprintf(&b, `"k0":"v0-%d"`, s)
	b.WriteString(`,"k1":12345`)
	b.WriteString(`,"k2":true`)
	b.WriteString(`,"k3":null`)
	b.WriteString(`,"k4":"a\"b\\cA"`)
	b.WriteString(`,"k5":-678.25`)
	fmt.Fprintf(&b, `,"k6":"utf8-é-%d"`, s)
	b.WriteString(`,"k7":""`)
	fmt.Fprintf(&b, `,"k8":"tail-%d"`, s)
	b.WriteString(`,"k9":0`)
	b.WriteString(`,"k10":false`)
	b.WriteString(`,"k11":"esc\u0041\u0042"`)
	b.WriteString(`,"k\u0031\u0032":"num-key"`)
	b.WriteByte('}')
	return b.Bytes()
}

func expectations(s int64) []expect {
	return []expect{
		{"k0", tokStr, "v0-" + strconv.FormatInt(s, 10), false},
		{"k1", tokNum, "12345", false},
		{"k2", tokBool, "true", false},
		{"k3", tokNull, "null", false},
		{"k4", tokStr, "a\"b\\cA", true},
		{"k5", tokNum, "-678.25", false},
		{"k6", tokStr, "utf8-é-" + strconv.FormatInt(s, 10), false},
		{"k7", tokStr, "", false},
		{"k8", tokStr, "tail-" + strconv.FormatInt(s, 10), false},
		{"k9", tokNum, "0", false},
		{"k10", tokBool, "false", false},
		{"k11", tokStr, "escAB", true},
		{"k12", tokStr, "num-key", false},
	}
}

func findToken(ts []token, key string) *token {
	for i := range ts {
		if ts[i].key == key {
			return &ts[i]
		}
	}
	return nil
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

	buf := buildJSON(seed)
	orig := append([]byte(nil), buf...)
	p := &parser{b: buf}
	toks, ok := p.parseObject()
	ck(ok, "parse object")
	p.ws()
	ck(p.i == len(buf), "parser consumed input")
	p.b = nil
	ck(bytes.Equal(buf, orig), "input immutable after parse")

	exps := expectations(seed)
	ck(len(toks) == len(exps), "token count")
	for i := 0; i < len(exps) && i < len(toks); i++ {
		t, e := toks[i], exps[i]
		ck(t.key == e.key, "key order i="+strconv.Itoa(i)+" got="+t.key)
		ck(t.kind == e.kind, "kind i="+strconv.Itoa(i))
		ck(t.val == e.val, "value i="+strconv.Itoa(i)+" got="+t.val)
		ck(t.copied == e.copied, "copied flag i="+strconv.Itoa(i))
		if !t.keyCopied && t.keyN > 0 {
			ck(aliases(buf, t.keyOff, t.key), "key aliases input i="+strconv.Itoa(i))
		}
		if !t.copied && (t.kind == tokStr || t.kind == tokNum) && t.n > 0 {
			ck(aliases(buf, t.off, t.val), "value aliases input i="+strconv.Itoa(i))
		}
		if t.copied {
			ck(!aliases(buf, t.off, t.val), "escaped value not aliased i="+strconv.Itoa(i))
		}
	}
	ck(toks[12].keyCopied, "escaped key marked copied")

	// 别名可见性：改输入字节，零拷贝视图立即变化；恢复后一致。
	t0 := findToken(toks, "k0")
	ck(t0 != nil, "find k0")
	if t0 != nil {
		old := string([]byte(t0.val))
		buf[t0.off] ^= 0xff
		ck(t0.val != old, "aliased token observes input write")
		buf[t0.off] ^= 0xff
		ck(t0.val == old, "aliased token restored")
	}
	t4 := findToken(toks, "k4")
	ck(t4 != nil, "find k4")
	if t4 != nil {
		old := "a\"b\\cA"
		buf[t4.off] ^= 0xff
		ck(t4.val == old, "copied escaped token isolated")
		buf[t4.off] ^= 0xff
		ck(t4.val == old, "copied escaped token stable")
	}
	ck(bytes.Equal(buf, orig), "input restored")

	// 丢弃输入，只保留 token 视图，跨 GC 校验内容。
	buf = nil
	orig = nil
	gcStress()
	for i, e := range exps {
		t := findToken(toks, e.key)
		ck(t != nil, "token after GC key="+e.key)
		if t == nil {
			continue
		}
		ck(t.kind == e.kind, "kind after GC i="+strconv.Itoa(i))
		ck(t.val == e.val, "value after GC key="+e.key)
		ck(t.key == e.key, "key after GC key="+e.key)
	}
	ck(len(toks) == 13, "all tokens retained")

	finish("l07_json_token_byte_alias")
}
