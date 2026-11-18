// L13: 变长批量命令的结构化编解码：1..15 条命令逐级增大，含 32KB 大参数；
// 解码结果是独立拷贝，编码缓冲被改写后仍有效。
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const magic = 0x01

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func appendUvarint(dst []byte, v uint64) []byte {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	return append(dst, tmp[:n]...)
}

func encodeBatch(cmds [][]string) []byte {
	out := make([]byte, 0, 64)
	out = append(out, magic)
	out = appendUvarint(out, uint64(len(cmds)))
	for _, args := range cmds {
		out = appendUvarint(out, uint64(len(args)))
		for _, a := range args {
			out = appendUvarint(out, uint64(len(a)))
			out = append(out, a...)
		}
	}
	return out
}

func readUvarint(b []byte, off *int) (uint64, bool) {
	v, n := binary.Uvarint(b[*off:])
	if n <= 0 {
		return 0, false
	}
	*off += n
	return v, true
}

func decodeBatch(b []byte) ([][]string, error) {
	if len(b) < 1 || b[0] != magic {
		return nil, fmt.Errorf("bad magic")
	}
	off := 1
	nc, ok := readUvarint(b, &off)
	if !ok {
		return nil, fmt.Errorf("truncated command count")
	}
	if nc > 1<<16 {
		return nil, fmt.Errorf("command count too large")
	}
	cmds := make([][]string, 0, nc)
	for i := uint64(0); i < nc; i++ {
		na, ok := readUvarint(b, &off)
		if !ok {
			return nil, fmt.Errorf("truncated arg count")
		}
		if na > 1<<12 {
			return nil, fmt.Errorf("arg count too large")
		}
		args := make([]string, 0, na)
		for j := uint64(0); j < na; j++ {
			l, ok := readUvarint(b, &off)
			if !ok {
				return nil, fmt.Errorf("truncated arg length")
			}
			if l > uint64(len(b)-off) {
				return nil, fmt.Errorf("arg length exceeds buffer")
			}
			args = append(args, string(b[off:off+int(l)]))
			off += int(l)
		}
		cmds = append(cmds, args)
	}
	if off != len(b) {
		return nil, fmt.Errorf("trailing bytes")
	}
	return cmds, nil
}

func payload(n int, seed uint64) string {
	b := make([]byte, n)
	x := seed*0x9E3779B97F4A7C15 + 7
	for i := range b {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		b[i] = byte('a' + (x>>24)%26)
	}
	return string(b)
}

func makeBatch(n int, seed uint64) [][]string {
	cmds := make([][]string, 0, n)
	for i := 0; i < n; i++ {
		nargs := 1 + i%4
		args := make([]string, 0, nargs+1)
		args = append(args, fmt.Sprintf("CMD%02d", i))
		for j := 0; j < nargs; j++ {
			var size int
			if (i+j)%7 == 0 {
				size = 0 // 空参数边界
			} else {
				size = 1 + (i*53+j*17+n*11+int(seed%97))%300
			}
			args = append(args, payload(size, seed+uint64(i*100+j)))
		}
		if n >= 10 && i == n-1 {
			args = append(args, payload(32768, seed+0xABCDEF))
		}
		cmds = append(cmds, args)
	}
	return cmds
}

func equalCmds(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 500 {
		n = 500
	}
	return n
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(1)

	for round := 0; round < rounds; round++ {
		for n := 1; n <= 15; n++ {
			seed := uint64(curSeed) + uint64(round*100+n)
			cmds := makeBatch(n, seed)
			enc := encodeBatch(cmds)
			if len(enc) <= 1 {
				ckSeed(false, fmt.Sprintf("n=%d encoded too short", n))
			}

			// 关键位置：编码完成后、解码前 GC，编码缓冲必须存活。
			if n%5 == 0 {
				gcStress()
			}
			dec, err := decodeBatch(enc)
			if err != nil {
				ckSeed(false, fmt.Sprintf("n=%d decode error: %v", n, err))
				continue
			}
			ckSeed(equalCmds(dec, cmds), fmt.Sprintf("n=%d roundtrip mismatch", n))

			// 解码结果是拷贝：改写编码缓冲并 GC 后仍必须一致。
			if n%4 == 0 {
				for i := range enc {
					enc[i] = 0xAA
				}
				gcStress()
				ckSeed(equalCmds(dec, cmds), fmt.Sprintf("n=%d decoded args independent of encoded buffer", n))
			}

			// 截断输入必须报错而不是 panic/越界。
			for _, cut := range []int{0, 1, 2, len(enc) / 2, len(enc) - 1} {
				if cut < 0 || cut >= len(enc) {
					continue
				}
				if _, err := decodeBatch(enc[:cut]); err == nil {
					ckSeed(false, fmt.Sprintf("n=%d truncated at %d accepted", n, cut))
				}
			}
		}
	}

	// 空批：0 条命令可往返。
	if dec, err := decodeBatch(encodeBatch(nil)); err != nil || len(dec) != 0 {
		ckSeed(false, "empty batch roundtrip")
	}

	// 畸形输入：坏 magic、超大长度、尾随字节。
	if _, err := decodeBatch([]byte{0x02}); err == nil {
		ckSeed(false, "bad magic accepted")
	}
	bad := []byte{magic}
	bad = appendUvarint(bad, 1) // 1 command
	bad = appendUvarint(bad, 1) // 1 arg
	bad = appendUvarint(bad, 1<<40)
	if _, err := decodeBatch(bad); err == nil {
		ckSeed(false, "oversize arg length accepted")
	}
	base := encodeBatch([][]string{{"A", "B"}})
	withTrail := append(append([]byte(nil), base...), 0xFF)
	if _, err := decodeBatch(withTrail); err == nil {
		ckSeed(false, "trailing byte accepted")
	}
	if _, err := decodeBatch(base); err != nil {
		ckSeed(false, fmt.Sprintf("base batch rejected: %v", err))
	}

	// 32KB 大参数跨 GC 的内容校验（单独跑一轮以确认大对象路径）。
	bigCmds := [][]string{{"BIG", payload(32768, uint64(curSeed)+999)}}
	bigEnc := encodeBatch(bigCmds)
	gcStress()
	bigDec, err := decodeBatch(bigEnc)
	ckSeed(err == nil && equalCmds(bigDec, bigCmds), "32KB arg roundtrip after GC")
	ckSeed(len(bigEnc) >= 32768 && bytes.Contains(bigEnc, []byte("BIG")), "large encoded batch shape")

	runtime.KeepAlive(bigEnc)
	runtime.KeepAlive(bigDec)
	finish("l13_pipeline_var_len")
}
