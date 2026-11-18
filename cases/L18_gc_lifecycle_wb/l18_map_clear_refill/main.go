// L18: 老 map 的 clear + 全量重填。每轮 noinline clear 清空老 map 并 GC，
// 再用年轻键值重新填满；GC 后按固定 key 顺序校验 map 条数与所有值。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	mcKeys    = 256
	mcRounds  = 8
	mcMaxRnds = 32
	mcDataN   = 20
)

type mcNode struct {
	id   int64
	ver  int
	name string
	data [mcDataN]byte
	kid  *mcNode
}

var (
	failed int32
	gcSink [][]byte
	mcMap  map[int]*mcNode
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
	if n > mcMaxRnds {
		n = mcMaxRnds
	}
	return n
}

func mcSalt(seed int64) int {
	s := int(seed % 223)
	if s < 0 {
		s = -s
	}
	return s
}

func mcByte(id int64, ver, i, salt int) byte {
	return byte((int(id%251)*19 + ver*31 + i*7 + salt) % 251)
}

//go:noinline
func mcMake(id int64, ver int, seed int64) *mcNode {
	n := &mcNode{id: id, ver: ver, name: fmt.Sprintf("m%d/%d", id%1000, ver)}
	salt := mcSalt(seed)
	for i := range n.data {
		n.data[i] = mcByte(id, ver, i, salt)
	}
	n.kid = &mcNode{id: id + 500000, ver: ver, name: fmt.Sprintf("kid%d", id%1000)}
	for i := range n.kid.data {
		n.kid.data[i] = mcByte(n.kid.id, ver, i, salt)
	}
	return n
}

//go:noinline
func mcClear(m map[int]*mcNode) { clear(m) }

//go:noinline
func mcRefill(m map[int]*mcNode, ver int, seed int64) {
	for k := 0; k < mcKeys; k++ {
		m[k] = mcMake(int64(k), ver, seed)
	}
}

func mcCheck(n *mcNode, id int64, ver int, seed int64, label string) bool {
	if n == nil || n.id != id || n.ver != ver || n.name != fmt.Sprintf("m%d/%d", id%1000, ver) {
		ck(false, fmt.Sprintf("%s: key %d value header corrupted (seed=%d)", label, id, seed))
		return false
	}
	salt := mcSalt(seed)
	for i := range n.data {
		if n.data[i] != mcByte(id, ver, i, salt) {
			ck(false, fmt.Sprintf("%s: key %d data corrupted at %d (seed=%d)", label, id, i, seed))
			return false
		}
	}
	kid := n.kid
	if kid == nil || kid.id != id+500000 || kid.ver != ver || kid.name != fmt.Sprintf("kid%d", id%1000) {
		ck(false, fmt.Sprintf("%s: key %d kid corrupted (seed=%d)", label, id, seed))
		return false
	}
	for i := range kid.data {
		if kid.data[i] != mcByte(kid.id, ver, i, salt) {
			ck(false, fmt.Sprintf("%s: key %d kid data corrupted at %d (seed=%d)", label, id, i, seed))
			return false
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(mcRounds)

	mcMap = make(map[int]*mcNode, mcKeys)
	mcRefill(mcMap, 1, seed)
	gcStress()
	gcStress() // map 晋升为老对象

	for r := 2; r <= rounds+1; r++ {
		mcClear(mcMap)
		gcStress()
		if len(mcMap) != 0 {
			ck(false, fmt.Sprintf("round %d: map len=%d after clear (seed=%d)", r, len(mcMap), seed))
			break
		}
		mcRefill(mcMap, r, seed)
		gcStress()
		if len(mcMap) != mcKeys {
			ck(false, fmt.Sprintf("round %d: map len=%d want=%d after refill (seed=%d)", r, len(mcMap), mcKeys, seed))
			break
		}
		bad := false
		for k := 0; k < mcKeys; k++ {
			if !mcCheck(mcMap[k], int64(k), r, seed, "refill") {
				bad = true
				break
			}
		}
		if bad {
			break
		}
	}

	finish("l18_map_clear_refill")
}
