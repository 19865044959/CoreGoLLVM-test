// L18: 老 map[*pkNode]*pkNode 的指针键删除/重插/覆写混合。
// 删除的老键仍由 retired slice 持有，替代键与值均为年轻对象；
// 每轮 GC 后按固定槽位顺序校验 map 条目、键值指针身份与逐字节 payload。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	pkSlots   = 64
	pkDel     = 16
	pkRounds  = 8
	pkMaxRnds = 32
	pkDataN   = 20
)

type pkNode struct {
	id   int64
	ver  int
	name string
	data [pkDataN]byte
}

var (
	failed    int32
	gcSink    [][]byte
	pkMap     map[*pkNode]*pkNode
	pkKeys    []*pkNode
	pkVals    []*pkNode
	pkKeyVer  []int
	pkValVer  []int
	pkRetired []*pkNode
	pkRetVer  []int
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
	if n > pkMaxRnds {
		n = pkMaxRnds
	}
	return n
}

func pkSalt(seed int64) int {
	s := int(seed % 251)
	if s < 0 {
		s = -s
	}
	return s
}

func pkByte(id int64, ver, i, salt int) byte {
	return byte((int(id%251)*17 + ver*23 + i*13 + salt) % 251)
}

//go:noinline
func pkMake(id int64, ver int, seed int64) *pkNode {
	n := &pkNode{id: id, ver: ver, name: fmt.Sprintf("p%d-%d", id%1000, ver)}
	salt := pkSalt(seed)
	for i := range n.data {
		n.data[i] = pkByte(id, ver, i, salt)
	}
	return n
}

//go:noinline
func pkDelete(m map[*pkNode]*pkNode, keys []*pkNode) {
	for _, k := range keys {
		delete(m, k)
	}
}

//go:noinline
func pkInsert(m map[*pkNode]*pkNode, k, v *pkNode) { m[k] = v }

//go:noinline
func pkOverwrite(m map[*pkNode]*pkNode, k, v *pkNode) { m[k] = v }

func pkCheck(n *pkNode, id int64, ver int, seed int64, label string) bool {
	if n == nil {
		ck(false, fmt.Sprintf("%s: node is nil, want id=%d ver=%d (seed=%d)", label, id, ver, seed))
		return false
	}
	if n.id != id || n.ver != ver || n.name != fmt.Sprintf("p%d-%d", id%1000, ver) {
		ck(false, fmt.Sprintf("%s: node id=%d ver=%d want=%d/%d (seed=%d)", label, n.id, n.ver, id, ver, seed))
		return false
	}
	salt := pkSalt(seed)
	for i := range n.data {
		if n.data[i] != pkByte(id, ver, i, salt) {
			ck(false, fmt.Sprintf("%s: node %d data corrupted at %d (seed=%d)", label, id, i, seed))
			return false
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(pkRounds)

	pkMap = make(map[*pkNode]*pkNode, pkSlots)
	pkKeys = make([]*pkNode, pkSlots)
	pkVals = make([]*pkNode, pkSlots)
	pkKeyVer = make([]int, pkSlots)
	pkValVer = make([]int, pkSlots)
	for i := 0; i < pkSlots; i++ {
		k := pkMake(int64(i), 1, seed)
		v := pkMake(int64(1000+i), 1, seed)
		pkMap[k] = v
		pkKeys[i], pkVals[i] = k, v
		pkKeyVer[i], pkValVer[i] = 1, 1
	}
	gcStress()
	gcStress() // map 与初始键值整体晋升为老对象

	rng := rand.New(rand.NewSource(seed))
	for r := 2; r <= rounds+1; r++ {
		perm := rng.Perm(pkSlots)
		del := perm[:pkDel]
		over := perm[pkDel : 2*pkDel]

		for _, s := range del {
			old := pkKeys[s]
			pkDelete(pkMap, []*pkNode{old})
			pkRetired = append(pkRetired, old)
			pkRetVer = append(pkRetVer, pkKeyVer[s])
			k := pkMake(int64(100000+r*100+s), r, seed)
			v := pkMake(int64(200000+r*100+s), r, seed)
			pkInsert(pkMap, k, v)
			pkKeys[s], pkVals[s] = k, v
			pkKeyVer[s], pkValVer[s] = r, r
		}
		for _, s := range over {
			v := pkMake(int64(300000+r*100+s), r, seed)
			pkOverwrite(pkMap, pkKeys[s], v)
			pkVals[s] = v
			pkValVer[s] = r
		}
		gcStress()

		if len(pkMap) != pkSlots {
			ck(false, fmt.Sprintf("round %d: map len=%d want=%d (seed=%d)", r, len(pkMap), pkSlots, seed))
			break
		}
		bad := false
		for s := 0; s < pkSlots; s++ {
			if got := pkMap[pkKeys[s]]; got != pkVals[s] {
				ck(false, fmt.Sprintf("round %d: slot %d value pointer mismatch (seed=%d)", r, s, seed))
				bad = true
				break
			}
			if !pkCheck(pkKeys[s], pkKeys[s].id, pkKeyVer[s], seed, "key") {
				bad = true
				break
			}
			if !pkCheck(pkVals[s], pkVals[s].id, pkValVer[s], seed, "value") {
				bad = true
				break
			}
		}
		if bad {
			break
		}
		for i, k := range pkRetired {
			if _, ok := pkMap[k]; ok {
				ck(false, fmt.Sprintf("round %d: retired key %d still in map (seed=%d)", r, i, seed))
				bad = true
				break
			}
			if !pkCheck(k, k.id, pkRetVer[i], seed, "retired-key") {
				bad = true
				break
			}
		}
		if bad {
			break
		}
	}

	finish("l18_map_ptr_key_churn")
}
