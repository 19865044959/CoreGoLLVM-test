// L13: slice 增长模式：append 扩容不变量、共享底层数组的 aliasing、满容量复制与指针保活。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type gnode struct {
	id   int
	name string
	pad  [24]byte
}

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

func newNode(id int) *gnode {
	n := &gnode{id: id, name: fmt.Sprintf("n%05d", id)}
	for i := range n.pad {
		n.pad[i] = byte(id*7 + i*3)
	}
	return n
}

func validNode(n *gnode, id int) bool {
	if n == nil || n.id != id || n.name != fmt.Sprintf("n%05d", id) {
		return false
	}
	for i := range n.pad {
		if n.pad[i] != byte(id*7+i*3) {
			return false
		}
	}
	return true
}

func validPtrs(ps []*gnode, nodes []*gnode, ids []int) bool {
	if len(ps) != len(ids) {
		return false
	}
	for i, id := range ids {
		if ps[i] != nodes[id] || !validNode(ps[i], id) {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	// 1) 指针切片从 nil 逐个 append 到 700：cap 单调不降，元素身份/内容跨 GC 不变。
	nodes := make([]*gnode, 0, 1200)
	for i := 0; i < 1200; i++ {
		nodes = append(nodes, newNode(i))
	}
	var ps []*gnode
	ids := make([]int, 0, 700)
	lastCap := 0
	for i := 0; i < 700; i++ {
		ps = append(ps, nodes[i])
		ids = append(ids, i)
		if cap(ps) < len(ps) {
			ck(false, "append growth: cap < len")
			break
		}
		if cap(ps) < lastCap {
			ck(false, "append growth: cap decreased")
			break
		}
		lastCap = cap(ps)
		if i%64 == 63 {
			gcStress()
			if !validPtrs(ps, nodes, ids) {
				ck(false, "growing pointer slice corrupted after GC")
				break
			}
		}
	}
	ck(cap(ps) >= len(ps) && len(ps) == 700, "append growth: final length/cap")
	gcStress()
	ck(validPtrs(ps, nodes, ids), "append growth: final pointer identity")

	// 2) 共享底层数组的 aliasing：append 在容量内写父数组但不改父长度。
	base := make([]*gnode, 4, 12)
	for i := 0; i < 4; i++ {
		base[i] = nodes[800+i]
	}
	baseIDs := []int{800, 801, 802, 803}
	sub := base[:2]
	subIDs := []int{800, 801}
	extra := nodes[900]
	sub = append(sub, extra)
	subIDs = append(subIDs, 900)
	ck(base[2] == extra, "alias: append into spare capacity not visible via parent index")
	ck(len(base) == 4, "alias: append changed parent length")
	baseIDs[2] = 900
	ck(validPtrs(base, nodes, baseIDs), "alias: parent content changed")
	gcStress()
	ck(base[2] == extra && validNode(extra, 900), "alias: child element lost after GC")
	ck(validPtrs(sub, nodes, subIDs), "alias: child view corrupted")

	// 3) 满容量 append 必须分配新数组；修改新数组不影响旧数组。
	full := make([]*gnode, 3, 3)
	for i := range full {
		full[i] = nodes[700+i]
	}
	fullIDs := []int{700, 701, 702}
	grown := append(full, nodes[950])
	ck(len(full) == 3 && len(grown) == 4, "full append: lengths")
	ck(grown[0] == full[0] && grown[2] == full[2], "full append: prefix identity changed")
	replacement := nodes[951]
	grown[0] = replacement
	ck(full[0] == nodes[700], "full append: writing new array changed old array index 0")
	ck(grown[0] == replacement, "full append: replacement not visible")
	fullIDs = append(fullIDs, 950)
	gcStress()
	ck(validPtrs(full, nodes, []int{700, 701, 702}), "full append: old array corrupted after GC")
	ck(validPtrs(grown, nodes, []int{951, 701, 702, 950}), "full append: new array corrupted after GC")

	// 4) 手工翻倍增长 []string：旧数组内容必须完整复制到新数组。
	var ss []string
	for i := 0; i < 300; i++ {
		if len(ss) == cap(ss) {
			nc := cap(ss) * 2
			if nc == 0 {
				nc = 4
			}
			ns := make([]string, len(ss), nc)
			copy(ns, ss)
			ss = ns
		}
		ss = append(ss, fmt.Sprintf("str-%04d", i))
	}
	for i := range ss {
		ck(ss[i] == fmt.Sprintf("str-%04d", i), "manual doubling: content lost")
	}
	gcStress()
	ck(ss[299] == "str-0299", "manual doubling: tail after GC")

	// 5) UAF_SEED 随机 append/截断：写指针复用底层数组，参考模型逐项校验。
	cur := make([]*gnode, 0, r.Intn(8)+1)
	curIDs := make([]int, 0, 64)
	nextID := 0
	for op := 0; op < 500; op++ {
		switch r.Intn(4) {
		case 0, 1:
			batch := r.Intn(7) + 1
			for j := 0; j < batch; j++ {
				cur = append(cur, nodes[nextID%1000])
				curIDs = append(curIDs, nextID%1000)
				nextID++
			}
		case 2:
			if len(cur) > 0 {
				keep := r.Intn(len(cur) + 1)
				cur = cur[:keep]
				curIDs = curIDs[:keep]
			}
		case 3:
			if len(cur) > 0 {
				cur[0] = nodes[(nextID+500)%1000]
				curIDs[0] = (nextID + 500) % 1000
				nextID++
			}
		}
		if cap(cur) < len(cur) {
			ckSeed(false, fmt.Sprintf("random growth: cap<len at op %d", op))
			break
		}
		if op%37 == 0 {
			gcStress()
			if !validPtrs(cur, nodes, curIDs) {
				ckSeed(false, fmt.Sprintf("random growth: pointers corrupted at op %d", op))
				break
			}
		}
	}
	gcStress()
	ckSeed(validPtrs(cur, nodes, curIDs), "random growth: final slice corrupted")

	runtime.KeepAlive(nodes)
	runtime.KeepAlive(ps)
	runtime.KeepAlive(ids)
	runtime.KeepAlive(base)
	runtime.KeepAlive(sub)
	runtime.KeepAlive(grown)
	runtime.KeepAlive(ss)
	runtime.KeepAlive(cur)
	finish("l13_slice_growth_alias")
}
