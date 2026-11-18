// L05: 对象图 + finalizer + 复活。每轮构建若干 18 节点 DAG（只保留向前边，
// 避免带 finalizer 的环不保证被回收）：一部分 pin 住并安装 finalizer（可达
// 期间不得触发），一部分整体丢弃后由 finalizer 异步上报；另有一个“凤凰”图
// 在 finalizer 中把根指针交回主协程，实现对象复活并跨 GC 校验。默认 2 轮，
// TORTURE_ROUNDS 可放大。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	fgNodes   = 18
	fgPinned  = 2
	fgDropped = 10
	fgPerRound = fgPinned + fgDropped + 1
)

var (
	failed    int32
	gcSink    [][]byte
	finCh     chan int
	phoenixCh chan *fgNode
)

type fgNode struct {
	id   int
	tag  string
	data []byte
	next *fgNode
	peer *fgNode
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckf(ok bool, format string, args ...interface{}) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: "+format+"\n", args...)
	}
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

func envInt(name string, def, lo, hi int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo {
		return def
	}
	if n > hi {
		return hi
	}
	return n
}

func finish(name string, seed uint64) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func gcStress() {
	for i := 0; i < 20; i++ {
		b := make([]byte, 16<<10)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func fgByte(round, g, node, j int, seed uint64) byte {
	return byte((seed*11 + uint64(round)*97 + uint64(g)*31 + uint64(node)*7 + uint64(j)*3) % 251)
}

func buildFG(round, g int, seed uint64) *fgNode {
	nodes := make([]*fgNode, fgNodes)
	for i := 0; i < fgNodes; i++ {
		data := make([]byte, 24+(i*11+g*5+round*3)%64)
		for j := range data {
			data[j] = fgByte(round, g, i, j, seed)
		}
		nodes[i] = &fgNode{
			id:   round*100000 + g*1000 + i,
			tag:  fmt.Sprintf("r%d-g%d-n%d", round, g, i),
			data: data,
		}
	}
	// 只加 i -> j (j>i) 的边，保证无环且整张图随根一起不可达。
	for i := 0; i+1 < fgNodes; i++ {
		nodes[i].next = nodes[i+1]
		if j := i + 7; j < fgNodes {
			nodes[i].peer = nodes[j]
		}
	}
	return nodes[0]
}

// fgDigest 用显式栈和 visited 处理环，只做确定性求和。
func fgDigest(root *fgNode) (int, uint64) {
	seen := make(map[*fgNode]bool, fgNodes)
	stack := []*fgNode{root}
	count := 0
	var sum uint64
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil || seen[n] {
			continue
		}
		seen[n] = true
		count++
		sum = sum*1099511628211 + uint64(n.id)*131
		for i := 0; i < len(n.tag); i++ {
			sum = sum*1099511628211 + uint64(n.tag[i])
		}
		for i := range n.data {
			sum = sum*1099511628211 + uint64(n.data[i])
		}
		if n.next != nil {
			stack = append(stack, n.next)
		}
		if n.peer != nil {
			stack = append(stack, n.peer)
		}
	}
	return count, sum
}

func main() {
	seed := loadSeed()
	rounds := envInt("TORTURE_ROUNDS", 2, 1, 8)
	finCh = make(chan int, 4096)
	phoenixCh = make(chan *fgNode, 64)

	pinned := make([]*fgNode, 0, rounds*fgPinned)
	pinnedDigest := make([]uint64, 0, rounds*fgPinned)
	pinnedID := make([]int, 0, rounds*fgPinned)
	phoenix := make([]*fgNode, 0, rounds)
	phoenixDigest := make(map[int]uint64)
	droppedIDs := make(map[int]bool)
	finIDs := make(map[int]int)
	finCount := 0

	for round := 0; round < rounds; round++ {
		for g := 0; g < fgPerRound; g++ {
			root := buildFG(round, g, seed)
			count, sum := fgDigest(root)
			ckf(count == fgNodes, "round=%d g=%d reachable=%d want=%d", round, g, count, fgNodes)

			switch {
			case g < fgPinned:
				runtime.SetFinalizer(root, func(n *fgNode) { finCh <- n.id })
				pinned = append(pinned, root)
				pinnedDigest = append(pinnedDigest, sum)
				pinnedID = append(pinnedID, root.id)
			case g == fgPerRound-1:
				phoenixDigest[root.id] = sum
				runtime.SetFinalizer(root, func(n *fgNode) { phoenixCh <- n })
			default:
				droppedIDs[root.id] = true
				runtime.SetFinalizer(root, func(n *fgNode) { finCh <- n.id })
			}
			root = nil // 除 pinned 外，其余图只由 finalizer 队列临时保活
		}

		gcStress()
		// 可达的 pin 图：内容完整，且 finalizer 不得上报。
		for i := 0; i < fgPinned; i++ {
			idx := round*fgPinned + i
			c, s := fgDigest(pinned[idx])
			ckf(c == fgNodes, "round=%d pinned=%d reachable=%d", round, i, c)
			ckf(s == pinnedDigest[idx], "round=%d pinned=%d digest changed after GC", round, i)
		}

		// finalizer 异步执行：循环触发 GC 并从通道收齐本轮应上报的对象。
		wantDropped := len(droppedIDs)
		wantPhoenix := round + 1
		deadline := time.Now().Add(8 * time.Second)
		for finCount < wantDropped || len(phoenix) < wantPhoenix {
			runtime.GC()
			select {
			case id := <-finCh:
				finCount++
				finIDs[id]++
			case n := <-phoenixCh:
				phoenix = append(phoenix, n)
			default:
				time.Sleep(time.Millisecond)
			}
			if time.Now().After(deadline) {
				ckf(false, "round=%d finalizer timeout got dropped=%d/%d phoenix=%d/%d",
					round, finCount, wantDropped, len(phoenix), wantPhoenix)
				break
			}
		}

		// 复活对象：finalizer 已运行，但根指针被交回后必须继续有效。
		for _, n := range phoenix {
			c, s := fgDigest(n)
			ckf(c == fgNodes, "phoenix id=%d reachable=%d", n.id, c)
			ckf(s == phoenixDigest[n.id], "phoenix id=%d digest changed after resurrection", n.id)
			ckf(n.next != nil && n.next.id == n.id+1, "phoenix id=%d forward link broken", n.id)
		}
	}

	// 丢弃全部 pin 图并再次 GC，确认复活对象与 pin 快照稳定。
	pinned = nil
	pinnedDigest = nil
	gcStress()
	for _, n := range phoenix {
		c, s := fgDigest(n)
		ckf(c == fgNodes, "final phoenix id=%d reachable=%d", n.id, c)
		ckf(s == phoenixDigest[n.id], "final phoenix id=%d digest changed", n.id)
	}

	// 审计 finalizer 上报集合：只能包含被丢弃的根，每个恰好一次。
	ckf(finCount == len(droppedIDs), "finalizer count=%d want=%d", finCount, len(droppedIDs))
	for id, n := range finIDs {
		ckf(n == 1, "finalizer id=%d ran %d times", id, n)
		ckf(droppedIDs[id], "finalizer id=%d is not a dropped root", id)
		for _, pid := range pinnedID {
			ckf(id != pid, "pinned id=%d was finalized while reachable", pid)
		}
	}
	ckf(len(phoenix) == rounds, "phoenix revived=%d want=%d", len(phoenix), rounds)

	finish("l05_graph_finalizer_revival", seed)
}
