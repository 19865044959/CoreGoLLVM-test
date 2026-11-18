// L18: 老无缓冲 chan 跨 goroutine 交接年轻链表，接收方把唯一引用放在
// 深递归栈帧上，强制 morestack 复制栈并在栈底做多轮 GC 后校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

const (
	ghListN   = 48
	ghDepth   = 320
	ghDataN   = 16
	ghRounds  = 4
	ghMaxRnds = 16
)

type ghNode struct {
	id   int64
	data [ghDataN]byte
	next *ghNode
}

type ghRes struct {
	ok  bool
	sum int64
}

var (
	failed int32
	gcSink [][]byte
	ghIn   chan *ghNode
	ghOut  chan ghRes
	ghSeed int64
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
	if n > ghMaxRnds {
		n = ghMaxRnds
	}
	return n
}

func ghSalt(seed int64) int {
	s := int(seed % 251)
	if s < 0 {
		s = -s
	}
	return s
}

func ghByte(seed, id int64, i int) byte {
	return byte((int(id%251)*31 + i*17 + ghSalt(seed)) % 251)
}

//go:noinline
func ghBuild(base, seed int64) (*ghNode, int64) {
	var head, prev *ghNode
	var sum int64
	for i := 0; i < ghListN; i++ {
		id := base + int64(i)
		n := &ghNode{id: id}
		for j := range n.data {
			n.data[j] = ghByte(seed, id, j)
		}
		if prev != nil {
			prev.next = n
		} else {
			head = n
		}
		prev = n
		sum += id
	}
	return head, sum
}

//go:noinline
func ghTouch(f [512]byte) byte { return f[511] }

//go:noinline
func ghDeep(n int, head *ghNode, seed int64) (bool, int64) {
	var frame [512]byte
	frame[0] = byte(n)
	if frame[0] != byte(n) || ghTouch(frame) != 0 {
		return false, 0
	}
	if n == 0 {
		runtime.GC()
		runtime.GC()
		return ghVerify(head, seed)
	}
	return ghDeep(n-1, head, seed)
}

//go:noinline
func ghVerify(head *ghNode, seed int64) (bool, int64) {
	var sum int64
	cur := head
	base := head.id
	for i := 0; i < ghListN; i++ {
		if cur == nil || cur.id != base+int64(i) {
			return false, 0
		}
		for j := range cur.data {
			if cur.data[j] != ghByte(seed, cur.id, j) {
				return false, 0
			}
		}
		sum += cur.id
		cur = cur.next
	}
	if cur != nil {
		return false, 0
	}
	return true, sum
}

func ghWorker() {
	for head := range ghIn {
		ok, sum := ghDeep(ghDepth, head, ghSeed)
		ghOut <- ghRes{ok: ok, sum: sum}
	}
	close(ghOut)
}

//go:noinline
func ghSendOnce(base, seed int64) int64 {
	head, sum := ghBuild(base, seed)
	ghIn <- head
	return sum
}

func main() {
	seed := seedValue()
	ghSeed = seed
	rounds := tortureRounds(ghRounds)

	ghIn = make(chan *ghNode)
	ghOut = make(chan ghRes)
	gcStress()
	gcStress() // 两个 chan 晋升为老对象

	go ghWorker()

	for r := 1; r <= rounds; r++ {
		base := int64(r * 1000)
		want := ghSendOnce(base, seed)
		// 发送完成后主协程不保留 head，唯一引用位于 worker 的递归栈帧上。
		runtime.GC()
		runtime.GC()
		res := <-ghOut
		ck(res.ok, fmt.Sprintf("round %d: worker stack copy lost list (seed=%d)", r, seed))
		ck(res.sum == want, fmt.Sprintf("round %d: checksum=%d want=%d (seed=%d)", r, res.sum, want, seed))
	}
	close(ghIn)
	for range ghOut {
	}
	gcStress()

	finish("l18_goroutine_stack_handoff")
}
