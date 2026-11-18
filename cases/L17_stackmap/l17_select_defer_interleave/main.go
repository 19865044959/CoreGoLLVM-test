// L17: select 与 defer 交错。每轮 select 取出含指针的 msg，随即注册两个 defer 捕获它；GC 落在这之间与 defer 内部。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	v    int64
	name string
	next *item
}

type msg struct {
	id    int64
	head  *item
	list  []*item
	label string
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
			if n > 512 {
				n = 512
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

func mkMsg(id int64) msg {
	return msg{
		id:    id,
		head:  &item{v: id*10 + 1, name: "head", next: &item{v: id*10 + 2, name: "tail"}},
		list:  []*item{{v: id*10 + 3, name: "l0"}, {v: id*10 + 4, name: "l1"}},
		label: "msg",
	}
}

func verifyMsg(m msg, where string) {
	bad := m.label != "msg" || m.head == nil || m.head.v != m.id*10+1 ||
		m.head.next == nil || m.head.next.v != m.id*10+2 ||
		len(m.list) != 2 || m.list[0].v != m.id*10+3 || m.list[1].v != m.id*10+4
	ck(!bad, fmt.Sprintf("%s: msg %d corrupted after GC seed=%d", where, m.id, uafSeed))
}

// drain 消费两个 channel 直到都为空；每个消息注册一个按值传参的 defer 和一个捕获
// per-iteration 局部的 defer，两者都在 select 循环结束后 LIFO 执行并再次 GC。
//
//go:noinline
func drain(a, b <-chan msg, wantCount int) (total int64, count int) {
	defer func() {
		gcStress()
		if count != wantCount {
			ck(false, fmt.Sprintf("drain count=%d want=%d seed=%d", count, wantCount, uafSeed))
		}
	}()
	loops := 0
	for {
		var m msg
		var ok bool
		if loops%4 == 0 {
			gcStress()
		}
		select {
		case m, ok = <-a:
		case m, ok = <-b:
		default:
			return
		}
		if !ok {
			ck(false, "drain: unexpected closed channel")
			return
		}
		loops++
		count++
		total += m.id

		cur := m
		defer func() {
			gcStress()
			verifyMsg(cur, "defer-captured")
			total += cur.id
		}()
		defer func(x msg) {
			gcStress()
			verifyMsg(x, "defer-arg")
		}(m)
	}
}

func main() {
	uafSeed = seedFromEnv()
	rounds := envRounds(60)

	for rnd := 0; rnd < rounds; rnd++ {
		perChan := 8
		a := make(chan msg, perChan)
		b := make(chan msg, perChan)
		var want int64
		base := int64(rnd*1000) + uafSeed%19
		for i := 0; i < perChan; i++ {
			idA := base + int64(i*2)
			idB := base + int64(i*2+1)
			a <- mkMsg(idA)
			b <- mkMsg(idB)
			want += idA + idB
		}
		gcStress()

		total, count := drain(a, b, perChan*2)
		ck(count == perChan*2, fmt.Sprintf("round %d count=%d want=%d seed=%d", rnd, count, perChan*2, uafSeed))
		ck(total == 2*want, fmt.Sprintf("round %d total=%d want=%d seed=%d", rnd, total, 2*want, uafSeed))
		gcStress()

		// 第二轮：只向一个 channel 写入，select 的 default 分支必须及时返回。
		c := make(chan msg, 4)
		d := make(chan msg, 4)
		var want2 int64
		for i := 0; i < 4; i++ {
			id := base + 500 + int64(i)
			c <- mkMsg(id)
			want2 += id
		}
		t2, n2 := drain(c, d, 4)
		ck(n2 == 4 && t2 == 2*want2, fmt.Sprintf("round %d single-chan total=%d count=%d seed=%d", rnd, t2, n2, uafSeed))
	}

	finish("l17_select_defer_interleave")
}
