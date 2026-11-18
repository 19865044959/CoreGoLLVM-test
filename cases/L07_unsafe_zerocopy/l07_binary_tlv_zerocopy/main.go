// L07: 零拷贝只读 TLV 解析：二进制记录 [tag u16][len u16][payload]，
// 叶子值用 unsafe.String 引用输入，嵌套容器递归解析，不写输入；视图跨 GC。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"unsafe"
)

const (
	tagMagic     = 0x0001
	tagLeaf      = 0x0010
	tagEmpty     = 0x0020
	tagBinary    = 0x0030
	tagContainer = 0x0100
	tagNested    = 0x0011
)

var (
	errDepth   = errors.New("tlv: max depth exceeded")
	errTrunc   = errors.New("tlv: truncated record")
	errTrail   = errors.New("tlv: trailing bytes")
	failed     int32
	gcSink     [][]byte
	seed       int64
	keepParsed []parsedSet
)

type tlvRec struct {
	tag   uint16
	raw   []byte
	value string
	depth int
}

type parsedSet struct {
	recs []tlvRec
	want []string
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, seed)
	}
}

func gcStress() {
	for r := 0; r < 2; r++ {
		for i := 0; i < 8; i++ {
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

func appendRec(dst []byte, tag uint16, payload []byte) []byte {
	dst = append(dst, byte(tag>>8), byte(tag), byte(len(payload)>>8), byte(len(payload)))
	dst = append(dst, payload...)
	return dst
}

func buildTLV(s int64) []byte {
	var top []byte
	top = appendRec(top, tagMagic, []byte("TLV1"))
	top = appendRec(top, tagLeaf, []byte("alpha-"+strconv.FormatInt(s%1000, 10)))
	top = appendRec(top, tagLeaf, []byte("beta-"+strconv.FormatInt((s+1)%1000, 10)))
	top = appendRec(top, tagEmpty, nil)
	top = appendRec(top, tagBinary, []byte{'n', 0, 'u', 'l', 'l', byte(s & 0xff)})
	var nested []byte
	nested = appendRec(nested, tagNested, []byte("nested-a-"+strconv.FormatInt(s%1000, 10)))
	nested = appendRec(nested, tagNested, []byte("nested-b-"+strconv.FormatInt((s+2)%1000, 10)))
	nested = appendRec(nested, tagEmpty, nil)
	nested = appendRec(nested, tagBinary, []byte{0, 1, 2, 3})
	top = appendRec(top, tagContainer, nested)
	return top
}

// parseTLV 只读取输入：raw 是输入子切片，value 用 unsafe.String 零拷贝引用 raw。
func parseTLV(buf []byte, depth, maxDepth int, out []tlvRec) ([]tlvRec, error) {
	if depth > maxDepth {
		return out, errDepth
	}
	pos := 0
	for pos < len(buf) {
		if len(buf)-pos < 4 {
			return out, errTrunc
		}
		tag := uint16(buf[pos])<<8 | uint16(buf[pos+1])
		n := int(buf[pos+2])<<8 | int(buf[pos+3])
		pos += 4
		if n > len(buf)-pos {
			return out, errTrunc
		}
		raw := buf[pos : pos+n]
		pos += n
		rec := tlvRec{tag: tag, raw: raw, depth: depth}
		if n > 0 {
			rec.value = unsafe.String(unsafe.SliceData(raw), len(raw))
		}
		out = append(out, rec)
		if tag == tagContainer {
			var err error
			out, err = parseTLV(raw, depth+1, maxDepth, out)
			if err != nil {
				return out, err
			}
		}
	}
	if pos != len(buf) {
		return out, errTrail
	}
	return out, nil
}

func aliases(a, b []byte) bool {
	return len(a) > 0 && len(b) > 0 &&
		uintptr(unsafe.Pointer(unsafe.SliceData(a))) < uintptr(unsafe.Pointer(unsafe.SliceData(b)))+uintptr(len(b)) &&
		uintptr(unsafe.Pointer(unsafe.SliceData(b))) < uintptr(unsafe.Pointer(unsafe.SliceData(a)))+uintptr(len(a))
}

func findByTag(recs []tlvRec, tag uint16) []tlvRec {
	out := make([]tlvRec, 0, 2)
	for i := range recs {
		if recs[i].tag == tag {
			out = append(out, recs[i])
		}
	}
	return out
}

func verifySet(buf, orig []byte, recs []tlvRec, s int64, stage string) {
	ck(bytes.Equal(buf, orig), stage+": input not modified")
	ck(len(recs) == 10, stage+": record count")

	// 顶层：magic, leaf, leaf, empty, binary, container；随后是 4 条嵌套记录。
	ck(recs[0].tag == tagMagic && recs[0].value == "TLV1", stage+": magic")
	leaves := findByTag(recs[:6], tagLeaf)
	ck(len(leaves) == 2, stage+": leaf count")
	ck(leaves[0].value == "alpha-"+strconv.FormatInt(s%1000, 10), stage+": alpha")
	ck(leaves[1].value == "beta-"+strconv.FormatInt((s+1)%1000, 10), stage+": beta")
	ck(leaves[0].depth == 0 && leaves[1].depth == 0, stage+": leaf depth")

	empty := findByTag(recs[:6], tagEmpty)
	ck(len(empty) == 1 && len(empty[0].raw) == 0 && empty[0].value == "" && empty[0].depth == 0,
		stage+": empty record")

	bin := findByTag(recs[:6], tagBinary)
	ck(len(bin) == 1 && len(bin[0].raw) == 6, stage+": binary length")
	if len(bin) == 1 {
		ck(bin[0].raw[0] == 'n' && bin[0].raw[1] == 0 && bin[0].raw[5] == byte(s&0xff), stage+": binary bytes")
		ck(bin[0].value == string(bin[0].raw), stage+": binary string view")
	}

	cont := findByTag(recs, tagContainer)
	ck(len(cont) == 1 && cont[0].depth == 0, stage+": container record")
	nested := make([]tlvRec, 0, 4)
	for i := range recs {
		if recs[i].depth == 1 {
			nested = append(nested, recs[i])
		}
	}
	ck(len(nested) == 4, stage+": nested count")
	if len(nested) == 4 {
		ck(nested[0].tag == tagNested && nested[0].value == "nested-a-"+strconv.FormatInt(s%1000, 10), stage+": nested a")
		ck(nested[1].tag == tagNested && nested[1].value == "nested-b-"+strconv.FormatInt((s+2)%1000, 10), stage+": nested b")
		ck(nested[2].tag == tagEmpty && len(nested[2].raw) == 0, stage+": nested empty")
		ck(nested[3].tag == tagBinary && len(nested[3].raw) == 4, stage+": nested binary")
	}

	// 零拷贝：非空 value 的 data 指针等于 raw 的起始地址，且 raw 与输入别名。
	for i := range recs {
		if len(recs[i].raw) == 0 {
			continue
		}
		ck(unsafe.StringData(recs[i].value) == unsafe.SliceData(recs[i].raw), stage+": value aliases raw")
		ck(aliases(recs[i].raw, buf), stage+": raw aliases input")
		ck(recs[i].value == string(recs[i].raw), stage+": value bytes")
	}
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
	rounds := 80
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			rounds = v
		}
	}

	for r := 0; r < rounds; r++ {
		s := seed + int64(r)*13
		buf := buildTLV(s)
		orig := append([]byte(nil), buf...)
		recs, err := parseTLV(buf, 0, 8, nil)
		ck(err == nil, "round "+strconv.Itoa(r)+": parse error")
		if err != nil {
			break
		}
		verifySet(buf, orig, recs, s, "round "+strconv.Itoa(r))

		if r%16 == 0 {
			want := make([]string, len(recs))
			for i := range recs {
				want[i] = string(recs[i].raw)
			}
			keepParsed = append(keepParsed, parsedSet{recs: recs, want: want})
		}

		// 只读解析后再解析一次，内容仍一致。
		recs2, err2 := parseTLV(buf, 0, 8, nil)
		ck(err2 == nil && len(recs2) == len(recs), "round "+strconv.Itoa(r)+": reparse")
		if err2 == nil {
			for i := range recs2 {
				ck(recs2[i].tag == recs[i].tag && recs2[i].value == recs[i].value, "round "+strconv.Itoa(r)+": reparse rec")
			}
		}

		// 畸形输入：长度越界 / 截断头 / 深度超限，均返回错误且不改写输入。
		bad := []byte{0x00, byte(tagLeaf), 0x01, 0x00, 'x'}
		badOrig := append([]byte(nil), bad...)
		if _, e := parseTLV(bad, 0, 8, nil); e == nil {
			ck(false, "round "+strconv.Itoa(r)+": truncated length accepted")
		}
		ck(bytes.Equal(bad, badOrig), "round "+strconv.Itoa(r)+": bad input not modified")
		if _, e := parseTLV([]byte{0, 1, 0}, 0, 8, nil); e == nil {
			ck(false, "round "+strconv.Itoa(r)+": truncated header accepted")
		}

		if r == 0 {
			deep := []byte{'d'}
			for i := 0; i < 12; i++ {
				deep = appendRec(nil, tagContainer, deep)
			}
			deepOrig := append([]byte(nil), deep...)
			if _, e := parseTLV(deep, 0, 3, nil); !errors.Is(e, errDepth) {
				ck(false, "depth limit not enforced")
			}
			ck(bytes.Equal(deep, deepOrig), "deep input not modified")
		}

		if r%8 == 0 {
			gcStress()
			verifySet(buf, orig, recs, s, "round "+strconv.Itoa(r)+" after GC")
		}
	}

	// 丢弃所有输入缓冲，仅保留解析视图；最终 GC 后内容仍正确。
	gcStress()
	for i := range keepParsed {
		set := &keepParsed[i]
		ck(len(set.recs) == len(set.want), "retained record count i="+strconv.Itoa(i))
		for j := range set.recs {
			ck(string(set.recs[j].raw) == set.want[j], "retained raw i="+strconv.Itoa(i)+" j="+strconv.Itoa(j))
			ck(set.recs[j].value == set.want[j], "retained value i="+strconv.Itoa(i)+" j="+strconv.Itoa(j))
			if len(set.recs[j].raw) > 0 {
				ck(unsafe.StringData(set.recs[j].value) == unsafe.SliceData(set.recs[j].raw), "retained alias")
			}
		}
	}

	runtime.KeepAlive(keepParsed)
	finish("l07_binary_tlv_zerocopy")
}
