// L18: 两条独立晋升的老双向链表，用年轻 bridge 节点拼接成一条链。
// 每轮可选择先断开（老->nil）再拼接（老->年轻->老），或直接替换 bridge；
// 每轮 GC 后从 head 正向、从 tail 反向全量校验双向链表与节点内容。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	obChainN    = 16
	obRounds    = 8
	obMaxRounds = 32
	obDataN     = 16
	obBridgeID  = 5000
)

type obNode struct {
	id   int64
	name string
	data [obDataN]byte
	next *obNode
	prev *obNode
}

type obHolder struct {
	aHead  *obNode
	aTail  *obNode
	bHead  *obNode
	bTail  *obNode
	bridge *obNode
}

var (
	failed int32
	gcSink [][]byte
	obH    obHolder
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
	if n > obMaxRounds {
		n = obMaxRounds
	}
	return n
}

func obByte(id int64, i int) byte { return byte((int(id)*29 + i*11) % 251) }

//go:noinline
func buildObChain(base int64) (head, tail *obNode) {
	var prev *obNode
	for i := 0; i < obChainN; i++ {
		id := base + int64(i)
		n := &obNode{id: id, name: "old"}
		for j := range n.data {
			n.data[j] = obByte(id, j)
		}
		n.prev = prev
		if prev == nil {
			head = n
		} else {
			prev.next = n
		}
		prev = n
	}
	return head, prev
}

//go:noinline
func obMakeBridge(ver int) *obNode {
	id := int64(obBridgeID + ver)
	n := &obNode{id: id, name: "bridge"}
	for i := range n.data {
		n.data[i] = obByte(id, i)
	}
	return n
}

//go:noinline
func obSetBridge(b, aTail, bHead *obNode) {
	aTail.next = b
	b.prev = aTail
	b.next = bHead
	bHead.prev = b
}

//go:noinline
func obDetach(aTail, bHead *obNode) {
	aTail.next = nil
	bHead.prev = nil
}

func obVerifyNode(n *obNode, id int64, name string, seed int64, label string) bool {
	if n == nil || n.id != id || n.name != name {
		ck(false, fmt.Sprintf("%s: node id/name corrupted want=%d/%s (seed=%d)", label, id, name, seed))
		return false
	}
	for i := range n.data {
		if n.data[i] != obByte(id, i) {
			ck(false, fmt.Sprintf("%s: node %d data corrupted at %d (seed=%d)", label, id, i, seed))
			return false
		}
	}
	return true
}

// obVerifyForward 从 start 正向遍历 want 序列，随后校验 prev 反向一致。
// names 为 nil 时所有节点名均为 "old"。
func obVerifyForward(start *obNode, want []int64, names []string, seed int64, label string) bool {
	cur := start
	var prev *obNode
	for i, id := range want {
		if cur == nil {
			ck(false, fmt.Sprintf("%s: chain ended at %d, want %d nodes (seed=%d)", label, i, len(want), seed))
			return false
		}
		name := "old"
		if names != nil {
			name = names[i]
		}
		if !obVerifyNode(cur, id, name, seed, label) {
			return false
		}
		if cur.prev != prev {
			ck(false, fmt.Sprintf("%s: forward prev link broken at %d (seed=%d)", label, i, seed))
			return false
		}
		prev = cur
		cur = cur.next
	}
	if cur != nil {
		ck(false, fmt.Sprintf("%s: chain longer than %d nodes (seed=%d)", label, len(want), seed))
		return false
	}
	// 从尾部反向走回，确认每个 next 回指。
	rev := prev
	for i := len(want) - 1; i >= 0; i-- {
		if rev == nil || rev.id != want[i] {
			ck(false, fmt.Sprintf("%s: reverse walk mismatch at %d (seed=%d)", label, i, seed))
			return false
		}
		if i > 0 && rev.prev == nil {
			ck(false, fmt.Sprintf("%s: reverse prev ended early at %d (seed=%d)", label, i, seed))
			return false
		}
		rev = rev.prev
	}
	if rev != nil {
		ck(false, fmt.Sprintf("%s: reverse walk did not terminate (seed=%d)", label, seed))
		return false
	}
	return true
}

func obChainIDs(base int64) []int64 {
	out := make([]int64, obChainN)
	for i := range out {
		out[i] = base + int64(i)
	}
	return out
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(obRounds)

	obH.aHead, obH.aTail = buildObChain(0)
	obH.bHead, obH.bTail = buildObChain(100)
	gcStress()
	gcStress() // 两条链整体晋升为老对象

	idsA := obChainIDs(0)
	idsB := obChainIDs(100)

	for r := 1; r <= rounds; r++ {
		// 种子奇偶决定本轮是否先断开再拼接；两条路径最终都必须得到同一条链。
		if (int(seed)+r)%2 == 0 {
			obDetach(obH.aTail, obH.bHead)
			obH.bridge = nil
			gcStress()
			obVerifyForward(obH.aHead, idsA, nil, seed, "detached A")
			obVerifyForward(obH.bHead, idsB, nil, seed, "detached B")
		}

		br := obMakeBridge(r)
		obSetBridge(br, obH.aTail, obH.bHead)
		obH.bridge = br
		gcStress()

		want := make([]int64, 0, 2*obChainN+1)
		names := make([]string, 0, 2*obChainN+1)
		want = append(want, idsA...)
		want = append(want, int64(obBridgeID+r))
		want = append(want, idsB...)
		for i := 0; i < obChainN; i++ {
			names = append(names, "old")
		}
		names = append(names, "bridge")
		for i := 0; i < obChainN; i++ {
			names = append(names, "old")
		}
		if !obVerifyForward(obH.aHead, want, names, seed, "merged") {
			break
		}
		if br.prev != obH.aTail || br.next != obH.bHead ||
			obH.aTail.next != br || obH.bHead.prev != br {
			ck(false, fmt.Sprintf("bridge %d endpoints corrupted (seed=%d)", br.id, seed))
			break
		}
		if !obVerifyNode(br, int64(obBridgeID+r), "bridge", seed, "bridge") {
			break
		}
	}

	finish("l18_old_chain_bridge_graft")
}
