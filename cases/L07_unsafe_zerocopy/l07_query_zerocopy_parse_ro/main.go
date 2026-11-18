// L07: 零拷贝 header/args 解析（不修改输入）：key/value 引用输入，百分号解码写独立缓冲。
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

type qpair struct {
	key     string
	val     string
	ks, ke  int
	vs, ve  int
	decoded bool
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
		for i := 0; i < 10; i++ {
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

func indexByteFrom(b []byte, from int, c byte) int {
	for i := from; i < len(b); i++ {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func needsDecode(b []byte) bool {
	for _, c := range b {
		if c == '%' || c == '+' {
			return true
		}
	}
	return false
}

// decodeForm 总是写入新分配的缓冲，绝不修改 src。
func decodeForm(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '+':
			out = append(out, ' ')
		case '%':
			if i+2 < len(src) {
				hi, lo := hexVal(src[i+1]), hexVal(src[i+2])
				if hi >= 0 && lo >= 0 {
					out = append(out, byte(hi<<4|lo))
					i += 2
					continue
				}
			}
			out = append(out, '%')
		default:
			out = append(out, src[i])
		}
	}
	return out
}

// parseQuery 只记录偏移并用 unsafe.String 引用 raw；编码值解码到新缓冲。
func parseQuery(raw []byte) []qpair {
	var out []qpair
	pos := 0
	for pos <= len(raw) {
		end := indexByteFrom(raw, pos, '&')
		if end < 0 {
			end = len(raw)
		}
		eq := indexByteFrom(raw[pos:end], 0, '=')
		var ks, ke, vs, ve int
		if eq < 0 {
			ks, ke = pos, end
			vs, ve = end, end
		} else {
			ks, ke = pos, pos+eq
			vs, ve = pos+eq+1, end
		}
		var key string
		if ke > ks {
			key = unsafe.String(&raw[ks], ke-ks)
		}
		var val string
		decoded := false
		if ve > vs {
			if needsDecode(raw[vs:ve]) {
				d := decodeForm(raw[vs:ve])
				if len(d) > 0 {
					val = unsafe.String(unsafe.SliceData(d), len(d))
				}
				decoded = true
			} else {
				val = unsafe.String(&raw[vs], ve-vs)
			}
		}
		out = append(out, qpair{key: key, val: val, ks: ks, ke: ke, vs: vs, ve: ve, decoded: decoded})
		if end == len(raw) {
			break
		}
		pos = end + 1
	}
	return out
}

func buildQuery(tag, n int) []byte {
	return []byte(fmt.Sprintf(
		"a=one-%02d&b=he%%20llo-%02d&c=&d=%%E4%%B8%%96-%02X&a=two-%02d&e=plain-%d&f=plus+sign-%d",
		tag%100, tag%100, tag%256, tag%100, n, tag%10))
}

func tortureRounds(def int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			return v
		}
	}
	return def
}

type retained struct {
	key, val string
	wantKey  string
	wantVal  string
	decoded  bool
}

func main() {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	if seed < 0 {
		seed = -seed
	}
	rounds := tortureRounds(120)

	var keep []retained
	for r := 0; r < rounds; r++ {
		tag := int(seed) + r
		n := (int(seed) + r*7) % 1000
		raw := buildQuery(tag, n)
		snap := append([]byte(nil), raw...)

		pairs := parseQuery(raw)
		ck(len(pairs) == 7, "pair count r="+strconv.Itoa(r))
		if len(pairs) != 7 {
			continue
		}

		// 解析器不得修改输入。
		ck(bytes.Equal(raw, snap), "input unchanged r="+strconv.Itoa(r))

		wantA1 := fmt.Sprintf("one-%02d", tag%100)
		wantB := fmt.Sprintf("he llo-%02d", tag%100)
		wantD := fmt.Sprintf("世-%02X", tag%256)
		wantA2 := fmt.Sprintf("two-%02d", tag%100)
		wantE := fmt.Sprintf("plain-%d", n)
		wantF := fmt.Sprintf("plus sign-%d", tag%10)
		type wantPair struct {
			key, val string
			decoded  bool
		}
		wants := []wantPair{
			{"a", wantA1, false},
			{"b", wantB, true},
			{"c", "", false},
			{"d", wantD, true},
			{"a", wantA2, false},
			{"e", wantE, false},
			{"f", wantF, true},
		}
		for i := range pairs {
			p := &pairs[i]
			ck(p.key == wants[i].key, "key i="+strconv.Itoa(i)+" r="+strconv.Itoa(r))
			ck(p.val == wants[i].val, "val i="+strconv.Itoa(i)+" r="+strconv.Itoa(r))
			ck(p.decoded == wants[i].decoded, "decoded flag i="+strconv.Itoa(i)+" r="+strconv.Itoa(r))

			// key 必须零拷贝引用输入。
			if p.ke > p.ks {
				ck(unsafe.Pointer(unsafe.StringData(p.key)) == unsafe.Pointer(&raw[p.ks]),
					"key aliases input i="+strconv.Itoa(i)+" r="+strconv.Itoa(r))
			}
			// 未编码值零拷贝；编码值的缓冲不指向输入区间。
			if p.ve > p.vs {
				if !p.decoded {
					ck(unsafe.Pointer(unsafe.StringData(p.val)) == unsafe.Pointer(&raw[p.vs]),
						"value aliases input i="+strconv.Itoa(i)+" r="+strconv.Itoa(r))
				} else {
					ck(unsafe.Pointer(unsafe.StringData(p.val)) != unsafe.Pointer(&raw[p.vs]),
						"decoded value separate i="+strconv.Itoa(i)+" r="+strconv.Itoa(r))
					if bytes.IndexByte(raw[p.vs:p.ve], '%') >= 0 {
						ck(len(p.val) != p.ve-p.vs,
							"percent-decoded length differs i="+strconv.Itoa(i)+" r="+strconv.Itoa(r))
					}
				}
			}
		}

		// 重复 key 的两个值彼此独立。
		ck(pairs[0].key == pairs[4].key && pairs[0].val != pairs[4].val, "duplicate key values")

		// 保留部分轮次的视图：只保留 key/value string，不保留 raw slice。
		if r%23 == 0 {
			for i := range pairs {
				keep = append(keep, retained{
					key: pairs[i].key, val: pairs[i].val,
					wantKey: wants[i].key, wantVal: wants[i].val,
					decoded: wants[i].decoded,
				})
			}
		}

		if r%8 == 7 {
			gcStress()
		}
	}

	// 最终 GC：保留视图必须各自保活其输入缓冲/解码缓冲。
	gcStress()
	ck(len(keep) > 0, "retained views non-empty")
	for i := range keep {
		ck(keep[i].key == keep[i].wantKey, "retained key i="+strconv.Itoa(i))
		ck(keep[i].val == keep[i].wantVal, "retained val i="+strconv.Itoa(i))
	}

	runtime.KeepAlive(keep)
	finish("l07_query_zerocopy_parse_ro")
}
