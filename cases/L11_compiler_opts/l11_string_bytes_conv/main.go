// L11: string/bytes 转换——两个方向的转换都必须复制内容；修改源字节或
// 转换结果不得影响已保存的字符串；Unicode、非法 UTF-8 与 rune 转换语义一致。
package main

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
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

func makeBytes(n int, salt byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte((i*37 + int(salt)) % 251)
	}
	if n >= 4 {
		b[1] = 0xff
		b[2] = 0xfe
		b[3] = 0x00
	}
	return b
}

func sameBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func checksum(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

func main() {
	seed = loadSeed()
	rounds := 6
	kept := make([]string, 0, rounds)
	keptWant := make([]uint64, 0, rounds)
	keptLen := make([]int, 0, rounds)

	for r := 0; r < rounds; r++ {
		n := 257 + int((seed+uint64(r)*19)%127)
		b := makeBytes(n, byte(seed)+byte(r)*7)
		orig := append([]byte(nil), b...)
		origSum := checksum(orig)

		s := string(b)
		ck(len(s) == len(orig), "string length mismatch")
		ck(sameBytes([]byte(s), orig), "string to bytes content mismatch")
		ck(bytes.Equal([]byte(s), orig), "bytes.Equal conversion mismatch")
		ck(bytes.Compare([]byte(s), orig) == 0, "bytes.Compare conversion mismatch")
		ck(string(b) == s, "repeated string conversion mismatch")

		// 转换后修改源字节：字符串必须已复制。
		for i := range b {
			b[i] = 0xAA
		}
		ck(sameBytes([]byte(s), orig), "string changed after mutating source bytes")
		ck(checksum([]byte(s)) == origSum, "string checksum changed after source mutation")

		// 转换后修改 []byte 结果：字符串必须不受影响。
		c := []byte(s)
		ck(sameBytes(c, orig), "bytes conversion content mismatch")
		c[0] ^= 0xFF
		c[len(c)-1] ^= 0x01
		sub := c[2:5]
		sub[0] ^= 0x40
		ck(sameBytes([]byte(s), orig), "string changed after mutating converted bytes")
		ck(checksum([]byte(s)) == origSum, "string checksum changed after converted bytes mutation")

		// append 形式与转换等价，且同样独立。
		ap := append([]byte(nil), s...)
		ck(sameBytes(ap, orig), "append from string mismatch")
		ap[0] ^= 0x80
		ck(sameBytes([]byte(s), orig), "string changed after mutating append copy")

		// 字符串作为 map 键必须保留创建时的内容。
		m := make(map[string]int64)
		m[s] = int64(r + 1)
		ck(m[string(orig)] == int64(r+1), "map key lookup after source mutation failed")
		ck(m[string(b)] == 0, "mutated source bytes produced original map key")
		ck(m[strings.Clone(s)] == int64(r+1), "map key lookup via cloned string failed")

		kept = append(kept, s)
		keptWant = append(keptWant, origSum)
		keptLen = append(keptLen, len(orig))
		gcStress()
	}

	// 空值与 nil 转换。
	var nilBytes []byte
	ck(string(nilBytes) == "", "nil bytes to string mismatch")
	ck(len([]byte("")) == 0, "empty string to bytes length mismatch")
	ck(len(string([]byte{})) == 0, "empty bytes to string length mismatch")
	ck(string([]byte{}) == "", "empty bytes to string content mismatch")

	// Unicode 与 rune 转换。
	us := "世界abc"
	ck(len([]byte(us)) == 9, "unicode byte length mismatch")
	ck(utf8.RuneCountInString(us) == 5, "unicode rune count mismatch")
	ck(string([]byte(us)) == us, "unicode bytes round trip mismatch")
	ck(string([]rune{'世', '界'}) == "世界", "rune slice conversion mismatch")
	ck(string(rune(0x65)) == "e", "rune int conversion mismatch")
	ck(string(rune(-1)) == "�", "negative rune conversion mismatch")
	ck(string(rune(0x110000)) == "�", "out-of-range rune conversion mismatch")
	ck(len(string(rune(0))) == 1 && string(rune(0)) == "\x00", "zero rune conversion mismatch")
	code := 66
	ck(string(code) == "B", "runtime int to string conversion mismatch")

	// 非法 UTF-8 字节必须原样保留（转换不做校验）。
	bad := []byte{0xff, 0xfe, 0x00, 0x80}
	sbad := string(bad)
	ck(len(sbad) == len(bad), "invalid UTF-8 byte length changed")
	ck(sameBytes([]byte(sbad), bad), "invalid UTF-8 bytes not preserved")
	ck(len([]rune(sbad)) == 4, "invalid UTF-8 rune count mismatch")

	// 保留的字符串跨最终 GC 后内容与长度不变。
	gcStress()
	for i := range kept {
		ck(len(kept[i]) == keptLen[i], "kept string length corrupted after GC")
		ck(checksum([]byte(kept[i])) == keptWant[i], "kept string checksum corrupted after GC")
	}

	finish("l11_string_bytes_conv")
}
