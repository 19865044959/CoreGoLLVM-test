// L07: 仿 fasthttp 用切片偏移 + unsafe.String 零拷贝解析 header，不修改输入，GC 后视图仍有效。
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

type field struct {
	key    string
	keyOff int
	val    []byte
	valOff int
}

type keyVal struct {
	k string
	v string
}

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
			b := make([]byte, 8192)
			b[0] = byte(i)
			b[len(b)-1] = byte(255 - i)
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

func buildRequest(s int64) []byte {
	var b []byte
	b = append(b, "GET /index.html?q=go HTTP/1.1\r\n"...)
	b = append(b, "Host: example.com:8080\r\n"...)
	b = append(b, "User-Agent:   uaf-l07/1.0  \r\n"...)
	b = append(b, "X-Trace-Id: 0123456789abcdef\r\n"...)
	b = append(b, "Accept-Encoding: gzip, deflate\r\n"...)
	b = append(b, "Content-Length: 42\r\n"...)
	b = append(b, "X-Empty:   \r\n"...)
	b = append(b, "X-Seed-Run: "...)
	b = strconv.AppendInt(b, s%97, 10)
	b = append(b, "\r\n\r\n"...)
	return b
}

func expectedFor(s int64) []keyVal {
	return []keyVal{
		{"Host", "example.com:8080"},
		{"User-Agent", "uaf-l07/1.0"},
		{"X-Trace-Id", "0123456789abcdef"},
		{"Accept-Encoding", "gzip, deflate"},
		{"Content-Length", "42"},
		{"X-Empty", ""},
		{"X-Seed-Run", strconv.FormatInt(s%97, 10)},
	}
}

// parseHeaders 只做偏移定位：key/value 都是输入缓冲的子切片/子串，绝不原地改写。
func parseHeaders(buf []byte, out []field) ([]field, error) {
	nl := bytes.IndexByte(buf, '\n')
	if nl < 0 {
		return out, fmt.Errorf("missing request line")
	}
	pos := nl + 1
	for pos < len(buf) {
		rel := bytes.IndexByte(buf[pos:], '\n')
		if rel < 0 {
			return out, fmt.Errorf("unterminated header at %d", pos)
		}
		lineStart := pos
		lineEnd := pos + rel
		line := buf[lineStart:lineEnd]
		pos = lineEnd + 1
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if len(line) == 0 {
			break
		}
		c := bytes.IndexByte(line, ':')
		if c <= 0 {
			return out, fmt.Errorf("bad header at %d", lineStart)
		}
		k := line[:c]
		khi := len(k)
		for khi > 0 && (k[khi-1] == ' ' || k[khi-1] == '\t') {
			khi--
		}
		if khi == 0 {
			return out, fmt.Errorf("empty header name at %d", lineStart)
		}
		vraw := line[c+1:]
		lo := 0
		for lo < len(vraw) && (vraw[lo] == ' ' || vraw[lo] == '\t') {
			lo++
		}
		hi := len(vraw)
		for hi > lo && (vraw[hi-1] == ' ' || vraw[hi-1] == '\t') {
			hi--
		}
		valOff := lineStart + c + 1 + lo
		out = append(out, field{
			key:    unsafe.String(unsafe.SliceData(k[:khi]), khi),
			keyOff: lineStart,
			val:    buf[valOff : valOff+(hi-lo)],
			valOff: valOff,
		})
	}
	return out, nil
}

func headerValue(fs []field, key string) ([]byte, bool) {
	for i := range fs {
		if fs[i].key == key {
			return fs[i].val, true
		}
	}
	return nil, false
}

func verify(buf, orig []byte, fs []field, s int64, stage string) {
	ck(bytes.Equal(buf, orig), stage+": input buffer not modified")
	want := expectedFor(s)
	ck(len(fs) == len(want), stage+": header count")
	for i := 0; i < len(fs) && i < len(want); i++ {
		f := fs[i]
		ck(f.key == want[i].k, fmt.Sprintf("%s: key %d", stage, i))
		ck(string(f.val) == want[i].v, fmt.Sprintf("%s: value %d", stage, i))
		ck(unsafe.StringData(f.key) == &buf[f.keyOff], fmt.Sprintf("%s: key %d aliases input", stage, i))
		if len(f.val) > 0 {
			ck(unsafe.SliceData(f.val) == &buf[f.valOff], fmt.Sprintf("%s: value %d aliases input", stage, i))
		}
	}
	if v, ok := headerValue(fs, "X-Trace-Id"); ok {
		ck(string(v) == "0123456789abcdef", stage+": lookup X-Trace-Id")
	} else {
		ck(false, stage+": lookup X-Trace-Id missing")
	}
	if v, ok := headerValue(fs, "X-Empty"); ok {
		ck(len(v) == 0, stage+": empty value preserved")
	} else {
		ck(false, stage+": lookup X-Empty missing")
	}
	if _, ok := headerValue(fs, "X-Missing"); ok {
		ck(false, stage+": unexpected X-Missing")
	}
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	rounds := 200
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			rounds = v
		}
	}

	var fields []field
	for r := 0; r < rounds; r++ {
		buf := buildRequest(seed)
		orig := append([]byte(nil), buf...)
		var err error
		fields, err = parseHeaders(buf, fields[:0])
		ck(err == nil, fmt.Sprintf("round %d: parse error %v", r, err))
		if err != nil {
			break
		}
		verify(buf, orig, fields, seed, fmt.Sprintf("round %d", r))
		if r%8 == 0 {
			gcStress()
			verify(buf, orig, fields, seed, fmt.Sprintf("round %d after GC", r))
		}
	}

	// 最后一轮持有的视图跨越 GC 后仍指向输入缓冲。
	lastBuf := buildRequest(seed)
	lastOrig := append([]byte(nil), lastBuf...)
	lastFields, err := parseHeaders(lastBuf, nil)
	if err != nil {
		ck(false, "final parse: "+err.Error())
	} else {
		gcStress()
		verify(lastBuf, lastOrig, lastFields, seed, "final after GC")
		v, ok := headerValue(lastFields, "Host")
		ck(ok && string(v) == "example.com:8080", "final Host lookup")
		runtime.KeepAlive(lastFields)
	}

	runtime.KeepAlive(fields)
	finish("l07_header_zero_copy_parse")
}
