// L15: 同一包内跨文件包级变量初始化。初始化器按依赖关系排序，而不是按
// 文件/声明顺序；tag 选中的文件插入依赖链，未选中的文件不参与。TORTURE_ROUNDS
// 轮在 GC 压力下遍历由这些变量构造的链表并做闭式校验。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type valueNode struct {
	id   int64
	next *valueNode
}

var (
	failed int32
	gcSink [][]byte
	// main.go 的变量依赖 tag 文件选中的 Tagged，初始化必然排在其后。
	mainTotal = Tagged + 1
	head      = &valueNode{id: Tagged, next: &valueNode{id: Fourth, next: &valueNode{id: Fifth}}}
)

// note 记录初始化器实际执行的顺序。
func note(name string, v int64) int64 {
	trace = append(trace, name)
	return v
}

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

func envInt(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return def
	}
	return n
}

func main() {
	tagName := "tagged"
	tagged := int64(35)
	if runtime.GOOS != "linux" {
		tagName = "tagged-other"
		tagged = 37
	}
	wantTrace := "beta,first,second,alpha,third,fifth,fourth," + tagName
	gotTrace := strings.Join(trace, ",")

	ck(gotTrace == wantTrace, fmt.Sprintf("var init order got=%q want=%q", gotTrace, wantTrace))
	ck(len(trace) == 8, fmt.Sprintf("var init count got=%d want=8", len(trace)))
	ck(Beta == 2 && First == 10 && Second == 15 && Alpha == 3 && Third == 20 && Fifth == 25 && Fourth == 30,
		fmt.Sprintf("base values got=%d,%d,%d,%d,%d,%d,%d", Beta, First, Second, Alpha, Third, Fifth, Fourth))
	ck(Tagged == tagged, fmt.Sprintf("tag-selected value got=%d want=%d", Tagged, tagged))
	ck(TaggedTail == tagged+1, fmt.Sprintf("tag tail got=%d want=%d", TaggedTail, tagged+1))
	ck(mainTotal == tagged+1, fmt.Sprintf("mainTotal got=%d want=%d", mainTotal, tagged+1))
	ck(head != nil && head.id == Tagged && head.next != nil && head.next.id == Fourth &&
		head.next.next != nil && head.next.next.id == Fifth, "initializer-built linked list")

	gcStress()
	ck(Tagged == tagged && TaggedTail == tagged+1 && mainTotal == tagged+1, "derived values after GC")
	ck(head != nil && head.id == Tagged && head.next != nil && head.next.id == Fourth &&
		head.next.next != nil && head.next.next.id == Fifth, "linked list after GC")

	rounds := envInt("TORTURE_ROUNDS", 5000)
	seed := envInt("UAF_SEED", 1)
	gcEvery := rounds / 16
	if gcEvery < 1 {
		gcEvery = 1
	}
	ids := [3]int64{Tagged, Fourth, Fifth}
	var sum int64
	for r := 0; r < rounds; r++ {
		start := (r + seed) % 3
		n := head
		for i := 0; i < start && n != nil; i++ {
			n = n.next
		}
		if n == nil || n.id != ids[start] {
			ck(false, fmt.Sprintf("chain corrupted round=%d start=%d seed=%d", r, start, seed))
			break
		}
		sum += n.id
		if r%128 == 0 {
			buf := make([]byte, 1024)
			buf[0] = byte(r)
			gcSink = append(gcSink[:0], buf)
		}
		if r%gcEvery == 0 {
			runtime.GC()
		}
	}
	cycle := ids[0] + ids[1] + ids[2]
	wantSum := int64(rounds/3) * cycle
	for i := 0; i < rounds%3; i++ {
		wantSum += ids[(seed+i)%3]
	}
	ck(sum == wantSum, fmt.Sprintf("torture checksum got=%d want=%d seed=%d rounds=%d", sum, wantSum, seed, rounds))

	runtime.GC()
	runtime.GC()
	ck(gotTrace == wantTrace, "var init trace unchanged after GC")
	ck(head != nil && head.id == Tagged && head.next != nil && head.next.id == Fourth &&
		head.next.next != nil && head.next.next.id == Fifth, "linked list after final GC")
	finish("l15_var_init_dep_files")
}
