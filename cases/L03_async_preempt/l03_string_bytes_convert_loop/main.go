// L03: 循环内 string/[]byte 双向转换。每轮 `s := string(b)` 后改 b 验证
// 字符串是独立副本；`b2 := []byte(s)` 后改 b2 验证字符串不被反向影响，
// 每 4 轮 append 后再转一次字符串。每 64 轮在 s/b2 仍存活时显式 GC，
// 结束后用独立的字节级模拟核对二进制转换结果与 FNV 累加值。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	sbFailed int32
)

const (
	sbSize    = 65536
	sbRounds  = 1024
	sbGCDiv   = 64
	sbHashLen = 256
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&sbFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&sbFailed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 1
}

func roundsFromEnv(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	if v > 1<<16 {
		v = 1 << 16
	}
	return v
}

func sbFill(b []byte, seed int64) {
	x := uint64(seed)*2862933555777941757 + 3037000493
	for i := range b {
		x = x*6364136223846793005 + 1442695040888963407
		b[i] = byte(x >> 35)
	}
}

func sbHashBytes(b []byte) uint64 {
	h := uint64(1469598103934665603)
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

func sbHashStr(s string) uint64 {
	h := uint64(1469598103934665603)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	rng := rand.New(rand.NewSource(seed))
	step := int64(2*rng.Intn(1023) + 1)
	off := int64(rng.Intn(sbSize))
	rounds := roundsFromEnv(sbRounds)

	b := make([]byte, sbSize)
	sbFill(b, seed)
	orig := append([]byte(nil), b...)
	h0 := sbHashBytes(b)

	acc := uint64(seed*31 + 7)
	expAcc := acc
	var bad, convCount, gcCount int64
	for r := 0; r < rounds; r++ {
		r64 := int64(r)
		s := string(b)
		if len(s) != sbSize || sbHashStr(s) != h0 {
			bad++
		}
		pos := int((r64*step + off) % sbSize)
		old := b[pos]
		b[pos] ^= 0xA5
		if s[pos] != old {
			bad++
		}
		b[pos] = old

		b2 := []byte(s)
		if len(b2) != sbSize || sbHashBytes(b2) != h0 {
			bad++
		}
		mpos := (pos + 1) % sbSize
		old2 := b2[mpos]
		b2[mpos] ^= 0x5A
		if s[mpos] != old2 {
			bad++
		}
		b2[0] ^= 0x11
		if s[0] != orig[0] {
			bad++
		}
		convCount += 2
		if r%4 == 0 {
			b2 = append(b2, byte(r64), byte(r64>>8), byte(r64>>16), byte(r64>>24),
				byte(r64>>32), byte(r64>>40), byte(r64>>48), byte(r64>>56))
			s2 := string(b2)
			wantEnd := orig[sbSize-1]
			if mpos == sbSize-1 {
				wantEnd ^= 0x5A
			}
			if len(s2) != sbSize+8 || s2[sbSize] != byte(r64) ||
				s2[sbSize-1] != wantEnd {
				bad++
			}
			convCount++
		}
		acc = acc*1099511628211 ^ sbHashBytes(b2[:sbHashLen])

		var tmp [sbHashLen]byte
		copy(tmp[:], orig[:sbHashLen])
		tmp[0] ^= 0x11
		if mpos < sbHashLen {
			tmp[mpos] ^= 0x5A
		}
		expAcc = expAcc*1099511628211 ^ sbHashBytes(tmp[:])

		if r%16 == 0 && sbHashBytes(b) != h0 {
			bad++
		}
		if r%sbGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(r)
			runtime.GC()
			gcCount++
			wantZero := orig[0] ^ 0x11
			wantM := orig[mpos] ^ 0x5A
			if mpos == 0 {
				wantZero ^= 0x5A
				wantM ^= 0x11
			}
			if s[0] != orig[0] || sbHashStr(s) != h0 {
				bad++
			}
			if b2[0] != wantZero || b2[mpos] != wantM {
				bad++
			}
			runtime.KeepAlive(s)
			runtime.KeepAlive(b2)
			runtime.KeepAlive(junk)
		}
	}

	ck(acc == expAcc, fmt.Sprintf("string/byte conversion checksum (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("conversion copies and immutability (seed=%d)", seed))
	ck(convCount == int64(rounds*2+(rounds+3)/4),
		fmt.Sprintf("conversion count (seed=%d)", seed))
	ck(gcCount == int64((rounds+sbGCDiv-1)/sbGCDiv),
		fmt.Sprintf("explicit GC count (seed=%d)", seed))
	ck(sbHashBytes(b) == h0, fmt.Sprintf("source bytes restored after loop (seed=%d)", seed))
	runtime.KeepAlive(b)
	runtime.KeepAlive(orig)
	finish("l03_string_bytes_convert_loop")
}
