// L17: 递归闭包 + GC。递归闭包构造带 parent 回指的树，GC 穿插在递归/遍历过程中。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type treeNode struct {
	id    int64
	label string
	kids  []*treeNode
	back  *treeNode
}

var (
	failed  int32
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	var sink [][]byte
	for i := 0; i < 2; i++ {
		b := make([]byte, 1024)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
	runtime.KeepAlive(sink)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil && v != 0 {
			return v
		}
	}
	return 1
}

func envRounds(def int) int {
	n := def
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n *= v
			if n > 256 {
				n = 256
			}
		}
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

// makeBuilder 返回递归闭包；calls 也被闭包捕获，每 gcEvery 次递归强制 GC。
func makeBuilder(gcEvery int, calls *int) func(int, int64, *treeNode) *treeNode {
	var build func(depth int, id int64, parent *treeNode) *treeNode
	build = func(depth int, id int64, parent *treeNode) *treeNode {
		n := &treeNode{id: id, label: "n", back: parent}
		*calls = *calls + 1
		if *calls%gcEvery == 0 {
			gcStress()
		}
		if depth > 0 {
			for k := 0; k < 2; k++ {
				n.kids = append(n.kids, build(depth-1, id*3+int64(k)+1, n))
			}
		}
		return n
	}
	return build
}

// makeWalker 返回递归遍历闭包；遍历途中周期性 GC，并校验 parent 回指。
func makeWalker(gcEvery int) func(*treeNode) (int, int64) {
	calls := 0
	var walk func(n *treeNode) (int, int64)
	walk = func(n *treeNode) (count int, sum int64) {
		if n == nil {
			ck(false, "walker: nil node")
			return 0, 0
		}
		calls++
		if calls%gcEvery == 0 {
			gcStress()
		}
		count = 1
		sum = n.id
		for _, k := range n.kids {
			if k == nil {
				ck(false, "walker: nil child")
				continue
			}
			if k.back != n {
				ck(false, fmt.Sprintf("walker: back pointer broken at id=%d seed=%d", n.id, uafSeed))
			}
			c, s := walk(k)
			count += c
			sum += s
		}
		return count, sum
	}
	return walk
}

func expectedTree(depth int, id int64) (int, int64) {
	count := 1
	sum := id
	if depth > 0 {
		for k := 0; k < 2; k++ {
			c, s := expectedTree(depth-1, id*3+int64(k)+1)
			count += c
			sum += s
		}
	}
	return count, sum
}

func verifyTree(n *treeNode, parent *treeNode, depth, maxDepth int) (int, int64) {
	if n == nil {
		ck(false, fmt.Sprintf("verify: nil node depth=%d", depth))
		return 0, 0
	}
	if n.back != parent {
		ck(false, fmt.Sprintf("verify: back pointer broken depth=%d id=%d seed=%d", depth, n.id, uafSeed))
	}
	if n.label != "n" {
		ck(false, fmt.Sprintf("verify: label corrupted depth=%d seed=%d", depth, uafSeed))
	}
	if depth < maxDepth && len(n.kids) != 2 {
		ck(false, fmt.Sprintf("verify: kids=%d depth=%d seed=%d", len(n.kids), depth, uafSeed))
	}
	count := 1
	sum := n.id
	for _, k := range n.kids {
		c, s := verifyTree(k, n, depth+1, maxDepth)
		count += c
		sum += s
	}
	return count, sum
}

func main() {
	uafSeed = seedFromEnv()
	rounds := envRounds(24)
	const depth = 10

	for rnd := 0; rnd < rounds; rnd++ {
		calls := 0
		gcEvery := 48 + int(uafSeed+int64(rnd))%32
		rootID := int64(rnd)*7 + uafSeed%5 + 1
		build := makeBuilder(gcEvery, &calls)
		root := build(depth, rootID, nil)
		ck(calls == 2047, fmt.Sprintf("round %d builder calls=%d want=2047 seed=%d", rnd, calls, uafSeed))

		gcStress()
		count, sum := verifyTree(root, nil, 0, depth)
		wantCount, wantSum := expectedTree(depth, rootID)
		ck(count == wantCount, fmt.Sprintf("round %d tree count=%d want=%d seed=%d", rnd, count, wantCount, uafSeed))
		ck(sum == wantSum, fmt.Sprintf("round %d tree sum=%d want=%d seed=%d", rnd, sum, wantSum, uafSeed))

		walk := makeWalker(32 + rnd)
		wc, ws := walk(root)
		gcStress()
		wc2, ws2 := walk(root)
		ck(wc == wantCount && wc2 == wantCount, fmt.Sprintf("round %d walker counts=%d/%d want=%d seed=%d", rnd, wc, wc2, wantCount, uafSeed))
		ck(ws == wantSum && ws2 == wantSum, fmt.Sprintf("round %d walker sums=%d/%d want=%d seed=%d", rnd, ws, ws2, wantSum, uafSeed))

		// 再 GC 一次后从 root 逐层取深度 2 的节点，确认回指与子节点仍有效。
		gcStress()
		if root == nil || root.back != nil || len(root.kids) != 2 {
			ck(false, fmt.Sprintf("round %d root corrupted after GC seed=%d", rnd, uafSeed))
		} else {
			child := root.kids[1]
			if child == nil || child.back != root || len(child.kids) != 2 || child.kids[0].back != child {
				ck(false, fmt.Sprintf("round %d child chain corrupted after GC seed=%d", rnd, uafSeed))
			}
		}
		root = nil
		runtime.GC()
	}

	finish("l17_recursive_closure_gc")
}
