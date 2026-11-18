// L18: 超过 32KB 的大对象仅由年轻 slice 持有，而该 slice 挂在老 holder 的
// interface 字段上。每轮 noinline setter 新建大对象并写入老接口；按种子保留
// 或丢弃上一代 slice，GC 后校验大对象零初始化、字段内容与存活。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	loN       = 4
	loRounds  = 4
	loMaxRnds = 16
	loDataN   = 6000
	loBufN    = 4096
	loNodeN   = 12
)

type loNode struct {
	id   int64
	ver  int
	name string
	data [loNodeN]byte
}

type loBig struct {
	data [loDataN]int64
	p    *loNode
	ifc  interface{}
	s    string
	tags [4]string
	buf  [loBufN]byte
}

type loHolder struct {
	ifc interface{}
	gen int
}

var (
	failed    int32
	gcSink    [][]byte
	loH       loHolder
	loPrev    []*loBig
	loPrevVer int
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
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

func seedValue() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
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
	if n > loMaxRnds {
		n = loMaxRnds
	}
	return n
}

func loSalt(seed int64) int {
	s := int(seed % 211)
	if s < 0 {
		s = -s
	}
	return s
}

func loVal(ver, idx, i, salt int) int64 {
	return int64((ver*100003 + idx*1009 + i*17 + salt) % 1000003)
}

func loByte(seed int64, id int64, i int) byte {
	return byte((int(id%251)*37 + i*19 + loSalt(seed)) % 251)
}

func loName(ver, idx int, suffix string) string {
	ch := byte('a' + (ver+idx)%26)
	return strings.Repeat(string(rune(ch)), 10+(ver+idx)%7) + strconv.Itoa(ver) + suffix
}

func loVerifyNode(n *loNode, id int64, ver int, wantName string, seed int64) bool {
	if n == nil || n.id != id || n.ver != ver || n.name != wantName {
		return false
	}
	for i := range n.data {
		if n.data[i] != loByte(seed, id, i) {
			return false
		}
	}
	return true
}

func loZeroCheck(b *loBig) bool {
	for i := range b.data {
		if b.data[i] != 0 {
			return false
		}
	}
	for i := range b.buf {
		if b.buf[i] != 0 {
			return false
		}
	}
	if b.p != nil || b.ifc != nil || b.s != "" || b.tags[0] != "" || b.tags[1] != "" || b.tags[2] != "" || b.tags[3] != "" {
		return false
	}
	return true
}

//go:noinline
func loMake(ver, idx int, seed int64) *loBig {
	b := &loBig{}
	if !loZeroCheck(b) {
		ck(false, fmt.Sprintf("fresh large object not zeroed (ver=%d idx=%d seed=%d)", ver, idx, seed))
		return nil
	}
	salt := loSalt(seed)
	for i := range b.data {
		b.data[i] = loVal(ver, idx, i, salt)
	}
	id := int64(ver*100 + idx)
	b.p = &loNode{id: id, ver: ver, name: loName(ver, idx, "")}
	for i := range b.p.data {
		b.p.data[i] = loByte(seed, id, i)
	}
	if idx%2 == 0 {
		q := &loNode{id: id + 50, ver: ver, name: loName(ver, idx, "-ifc")}
		for i := range q.data {
			q.data[i] = loByte(seed, id+50, i)
		}
		b.ifc = q
	} else {
		blob := make([]byte, 32)
		for i := range blob {
			blob[i] = loByte(seed, id+77, i)
		}
		b.ifc = blob
	}
	b.s = loName(ver, idx, "-s")
	for t := range b.tags {
		b.tags[t] = loName(ver, idx, fmt.Sprintf("-t%d", t))
	}
	for i := range b.buf {
		b.buf[i] = loByte(seed, id+99, i)
	}
	return b
}

//go:noinline
func loSet(h *loHolder, ver int, seed int64) []*loBig {
	prev, _ := h.ifc.([]*loBig)
	next := make([]*loBig, loN)
	for i := range next {
		next[i] = loMake(ver, i, seed)
	}
	h.ifc = next
	h.gen = ver
	return prev
}

func loVerify(b *loBig, ver, idx int, seed int64, label string) bool {
	if b == nil {
		ck(false, fmt.Sprintf("%s: large object %d nil (seed=%d)", label, idx, seed))
		return false
	}
	salt := loSalt(seed)
	for i := range b.data {
		if b.data[i] != loVal(ver, idx, i, salt) {
			ck(false, fmt.Sprintf("%s: large object %d data corrupted at %d (seed=%d)", label, idx, i, seed))
			return false
		}
	}
	id := int64(ver*100 + idx)
	if !loVerifyNode(b.p, id, ver, loName(ver, idx, ""), seed) {
		ck(false, fmt.Sprintf("%s: large object %d node corrupted (seed=%d)", label, idx, seed))
		return false
	}
	if idx%2 == 0 {
		q, ok := b.ifc.(*loNode)
		if !ok || !loVerifyNode(q, id+50, ver, loName(ver, idx, "-ifc"), seed) {
			ck(false, fmt.Sprintf("%s: large object %d interface node corrupted (seed=%d)", label, idx, seed))
			return false
		}
	} else {
		blob, ok := b.ifc.([]byte)
		if !ok || len(blob) != 32 {
			ck(false, fmt.Sprintf("%s: large object %d interface blob corrupted (seed=%d)", label, idx, seed))
			return false
		}
		for i := range blob {
			if blob[i] != loByte(seed, id+77, i) {
				ck(false, fmt.Sprintf("%s: large object %d blob data corrupted at %d (seed=%d)", label, idx, i, seed))
				return false
			}
		}
	}
	if b.s != loName(ver, idx, "-s") {
		ck(false, fmt.Sprintf("%s: large object %d string corrupted (seed=%d)", label, idx, seed))
		return false
	}
	for t := range b.tags {
		if b.tags[t] != loName(ver, idx, fmt.Sprintf("-t%d", t)) {
			ck(false, fmt.Sprintf("%s: large object %d tag %d corrupted (seed=%d)", label, idx, t, seed))
			return false
		}
	}
	for i := range b.buf {
		if b.buf[i] != loByte(seed, id+99, i) {
			ck(false, fmt.Sprintf("%s: large object %d buf corrupted at %d (seed=%d)", label, idx, i, seed))
			return false
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(loRounds)

	loSet(&loH, 1, seed)
	gcStress()
	gcStress() // holder/interface 晋升为老对象，第一代大对象一并提升

	for r := 2; r <= rounds+1; r++ {
		prev := loSet(&loH, r, seed)
		keep := (int(seed)+r)%2 == 0
		if keep {
			loPrev = prev
			loPrevVer = r - 1
		} else {
			loPrev = nil
			loPrevVer = 0
		}
		gcStress()

		cur, ok := loH.ifc.([]*loBig)
		if !ok || len(cur) != loN || loH.gen != r {
			ck(false, fmt.Sprintf("round %d: holder interface gen=%d type/size broken (seed=%d)", r, loH.gen, seed))
			break
		}
		bad := false
		for i := 0; i < loN; i++ {
			if !loVerify(cur[i], r, i, seed, "live") {
				bad = true
				break
			}
		}
		if bad {
			break
		}
		if loPrev != nil {
			if len(loPrev) != loN {
				ck(false, fmt.Sprintf("round %d: retired slice size broken (seed=%d)", r, seed))
				break
			}
			for i := 0; i < loN; i++ {
				if !loVerify(loPrev[i], loPrevVer, i, seed, "retired") {
					bad = true
					break
				}
			}
		}
		if bad {
			break
		}
	}

	finish("l18_large_obj_young_owner")
}
