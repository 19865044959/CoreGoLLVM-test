// L18: 五层跨代阶梯 + 每轮追加年轻 cohort。初始 5 个 cohort 分别经历不同
// 次数 GC 晋升；阶梯链、peers map 与 interface 同时保存跨代引用（老->新、
// 新->老、老->老）。每轮把老 tail 连到年轻 cohort，并由老 map/interface
// 指向年轻节点，GC 后沿整条阶梯校验 ID、链接、代次与跨代别名。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	cg2Cols   = 6
	cg2Base   = 5
	cg2Rounds = 6
	cg2MaxRnd = 30
	cg2DataN  = 24
)

type cg2Node struct {
	id     int64
	cohort int
	name   string
	data   [cg2DataN]byte
	next   *cg2Node
	prev   *cg2Node
	peers  map[string]*cg2Node
	any    interface{}
}

type cg2Ladder struct {
	head    *cg2Node
	tail    *cg2Node
	cohorts int
}

var (
	failed int32
	gcSink [][]byte
	cg2H   cg2Ladder
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
	if n > cg2MaxRnd {
		n = cg2MaxRnd
	}
	return n
}

func cg2Byte(id, salt int64, i int) byte {
	return byte((int(id)*67 + int(salt)*29 + i*11) % 251)
}

//go:noinline
func cg2NewNode(cohort, col int, salt int64) *cg2Node {
	id := int64(cohort*100 + col + 1)
	n := &cg2Node{id: id, cohort: cohort, name: "c" + strconv.Itoa(cohort)}
	for i := range n.data {
		n.data[i] = cg2Byte(id, salt, i)
	}
	n.peers = map[string]*cg2Node{}
	return n
}

//go:noinline
func cg2MakeCohort(cohort int, salt int64) []*cg2Node {
	nodes := make([]*cg2Node, cg2Cols)
	for i := range nodes {
		nodes[i] = cg2NewNode(cohort, i, salt)
	}
	return nodes
}

//go:noinline
func cg2LinkTail(tail, head *cg2Node) {
	tail.next = head
	head.prev = tail
}

//go:noinline
func cg2SetIface(old, y *cg2Node) { old.any = y }

//go:noinline
func cg2SetMap(old *cg2Node, key string, y *cg2Node) { old.peers[key] = y }

//go:noinline
func cg2SetMapOld(y *cg2Node, key string, old *cg2Node) { y.peers[key] = old }

//go:noinline
func cg2Build(salt int64) {
	var cohorts [cg2Base][]*cg2Node
	for c := 0; c < cg2Base; c++ {
		cohorts[c] = cg2MakeCohort(c, salt)
		gcStress() // 每个 cohort 依创建顺序获得不同的“年龄”
	}
	for c := 0; c < cg2Base; c++ {
		for i, n := range cohorts[c] {
			if c > 0 {
				n.peers["prev"] = cohorts[c-1][i]
			}
			if c < cg2Base-1 {
				n.peers["next"] = cohorts[c+1][i]
			}
			switch c {
			case 0:
				n.any = cohorts[2][i]
			case 1:
				n.any = cohorts[3][i]
			case 2:
				n.any = cohorts[4][i]
			}
			if i+1 < cg2Cols {
				n.next = cohorts[c][i+1]
			} else if c+1 < cg2Base {
				n.next = cohorts[c+1][0]
			}
			if i > 0 {
				n.prev = cohorts[c][i-1]
			} else if c > 0 {
				n.prev = cohorts[c-1][cg2Cols-1]
			}
		}
	}
	cg2H.head = cohorts[0][0]
	cg2H.tail = cohorts[cg2Base-1][cg2Cols-1]
	cg2H.cohorts = cg2Base
	gcStress() // 跨代链接与 interface 在 GC 后必须保持
}

// cg2Collect 沿 next 链收集所有节点，供 grow/verify 使用。
//
//go:noinline
func cg2Collect() []*cg2Node {
	var out []*cg2Node
	for n := cg2H.head; n != nil; n = n.next {
		out = append(out, n)
		if len(out) > (cg2Base+cg2MaxRnd)*cg2Cols {
			return nil
		}
	}
	return out
}

//go:noinline
func cg2Grow(ver int, salt int64) {
	all := cg2Collect()
	if all == nil || len(all) != cg2H.cohorts*cg2Cols {
		ck(false, fmt.Sprintf("grow: chain broken before append (seed=%d)", salt))
		return
	}
	cohort := cg2H.cohorts
	nodes := cg2MakeCohort(cohort, salt)
	for i := 0; i+1 < len(nodes); i++ {
		nodes[i].next = nodes[i+1]
		nodes[i+1].prev = nodes[i]
	}
	for i, n := range nodes {
		cg2SetMapOld(n, "old", all[i])
		cg2SetMapOld(n, "prevgen", all[(cohort-1)*cg2Cols+i])
		n.any = all[i]
		cg2SetIface(all[2*cg2Cols+i], n)
		cg2SetMap(all[i], "young", n)
		cg2SetMap(all[4*cg2Cols+i], "next", n)
	}
	cg2LinkTail(cg2H.tail, nodes[0])
	cg2H.tail = nodes[cg2Cols-1]
	cg2H.cohorts++
}

func cg2CheckNode(n *cg2Node, cohort, col int, salt int64) bool {
	id := int64(cohort*100 + col + 1)
	if n == nil || n.id != id || n.cohort != cohort || n.name != "c"+strconv.Itoa(cohort) {
		ck(false, fmt.Sprintf("ladder node cohort=%d col=%d header corrupted (salt=%d)", cohort, col, salt))
		return false
	}
	for i := range n.data {
		if n.data[i] != cg2Byte(id, salt, i) {
			ck(false, fmt.Sprintf("ladder node id=%d data corrupted at %d (salt=%d)", id, i, salt))
			return false
		}
	}
	return true
}

func cg2Verify(salt int64) bool {
	all := cg2Collect()
	last := cg2H.cohorts - 1
	if all == nil || len(all) != (last+1)*cg2Cols {
		ck(false, fmt.Sprintf("ladder length=%d want=%d (salt=%d)", len(all), (last+1)*cg2Cols, salt))
		return false
	}
	for k, n := range all {
		cohort := k / cg2Cols
		col := k % cg2Cols
		if !cg2CheckNode(n, cohort, col, salt) {
			return false
		}
		if k > 0 && n.prev != all[k-1] {
			ck(false, fmt.Sprintf("ladder prev link broken at k=%d (salt=%d)", k, salt))
			return false
		}
		if k+1 < len(all) && n.next != all[k+1] {
			ck(false, fmt.Sprintf("ladder next link broken at k=%d (salt=%d)", k, salt))
			return false
		}
	}
	if all[len(all)-1].next != nil || all[0].prev != nil {
		ck(false, fmt.Sprintf("ladder endpoints corrupted (salt=%d)", salt))
		return false
	}

	for c := 0; c <= last; c++ {
		for i := 0; i < cg2Cols; i++ {
			n := all[c*cg2Cols+i]
			if c > 0 && c <= 4 && n.peers["prev"] != all[(c-1)*cg2Cols+i] {
				ck(false, fmt.Sprintf("peers prev broken c=%d i=%d (salt=%d)", c, i, salt))
				return false
			}
			if c > 0 && c < 4 && n.peers["next"] != all[(c+1)*cg2Cols+i] {
				ck(false, fmt.Sprintf("peers next broken c=%d i=%d (salt=%d)", c, i, salt))
				return false
			}
			if c == 4 {
				var wantNext *cg2Node
				if last > 4 {
					wantNext = all[last*cg2Cols+i]
				}
				if n.peers["next"] != wantNext {
					ck(false, fmt.Sprintf("old tail map->young broken i=%d (salt=%d)", i, salt))
					return false
				}
			}
			if c > 4 && n.peers["next"] != nil {
				ck(false, fmt.Sprintf("young cohort unexpected next map c=%d i=%d (salt=%d)", c, i, salt))
				return false
			}
			if c == 0 {
				var wantYoung *cg2Node
				if last > 4 {
					wantYoung = all[last*cg2Cols+i]
				}
				if n.peers["young"] != wantYoung {
					ck(false, fmt.Sprintf("old map->young broken i=%d (salt=%d)", i, salt))
					return false
				}
				if in, ok := n.any.(*cg2Node); !ok || in != all[2*cg2Cols+i] {
					ck(false, fmt.Sprintf("cohort0 interface cross link broken i=%d (salt=%d)", i, salt))
					return false
				}
			}
			if c == 1 {
				if in, ok := n.any.(*cg2Node); !ok || in != all[3*cg2Cols+i] {
					ck(false, fmt.Sprintf("cohort1 interface cross link broken i=%d (salt=%d)", i, salt))
					return false
				}
			}
			if c == 2 {
				if in, ok := n.any.(*cg2Node); !ok || in != all[last*cg2Cols+i] {
					ck(false, fmt.Sprintf("old interface->youngest broken i=%d (salt=%d)", i, salt))
					return false
				}
			}
			if c == 3 || c == 4 {
				if n.any != nil {
					ck(false, fmt.Sprintf("cohort%d unexpected interface c=%d i=%d (salt=%d)", c, c, i, salt))
					return false
				}
			}
			if c > 4 {
				if n.peers["old"] != all[i] {
					ck(false, fmt.Sprintf("young map->old broken c=%d i=%d (salt=%d)", c, i, salt))
					return false
				}
				if n.peers["prevgen"] != all[(c-1)*cg2Cols+i] {
					ck(false, fmt.Sprintf("young map->prevgen broken c=%d i=%d (salt=%d)", c, i, salt))
					return false
				}
				if in, ok := n.any.(*cg2Node); !ok || in != all[i] {
					ck(false, fmt.Sprintf("young interface->old broken c=%d i=%d (salt=%d)", c, i, salt))
					return false
				}
			}
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(cg2Rounds)
	salt := seed % 13

	cg2Build(salt)
	if !cg2Verify(salt) {
		finish("l18_cross_generation_ladder")
		return
	}
	for r := 1; r <= rounds; r++ {
		cg2Grow(r, salt)
		gcStress()
		if !cg2Verify(salt) {
			break
		}
	}

	finish("l18_cross_generation_ladder")
}
