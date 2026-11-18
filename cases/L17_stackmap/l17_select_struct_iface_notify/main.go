// L17: select + chan struct{} 通知 + interface 变参。四条唯一就绪路径上注册 defer，
// case body 经接口转发 []any；notify 内与 defer 内各 GC 并跨校验。
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
	tag  string
	next *item
}

type pair struct {
	p *item
	s string
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

//go:noinline
func gcStress() {
	var sink [][]byte
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		sink = append(sink, b)
	}
	runtime.GC()
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

func envRounds(def, max int) int {
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			if v > max {
				return max
			}
			return v
		}
	}
	return def
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%d\n", name, n, uafSeed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func mkItem(base int64) *item {
	return &item{v: base, tag: "sel", next: &item{v: base + 5, tag: "sel-next"}}
}

func itemVal(x *item, where string) int64 {
	if x == nil {
		ck(false, fmt.Sprintf("%s: nil item seed=%d", where, uafSeed))
		return 0
	}
	if x.tag != "sel" || x.next == nil || x.next.tag != "sel-next" || x.next.v != x.v+5 {
		ck(false, fmt.Sprintf("%s: item chain corrupted seed=%d", where, uafSeed))
	}
	return x.v + x.next.v
}

func scanAny(a any, where string, idx int) int64 {
	switch x := a.(type) {
	case nil:
		return 0
	case *item:
		return itemVal(x, fmt.Sprintf("%s[%d]", where, idx))
	case pair:
		return itemVal(x.p, fmt.Sprintf("%s[%d].p", where, idx)) + int64(len(x.s))
	case []*item:
		var sum int64
		for i, p := range x {
			sum += itemVal(p, fmt.Sprintf("%s[%d].list[%d]", where, idx, i))
		}
		return sum
	case string:
		return int64(len(x))
	case int64:
		return x
	default:
		ck(false, fmt.Sprintf("%s: unexpected type %T at %d seed=%d", where, a, idx, uafSeed))
		return 0
	}
}

func scanArgs(vals []any, where string) int64 {
	var sum int64
	for i, v := range vals {
		sum += scanAny(v, where, i)
	}
	return sum
}

type notifier interface {
	Notify(kind string, vals ...any) int64
}

type impl struct {
	keep *item
	tag  string
}

func (n *impl) Notify(kind string, vals ...any) int64 {
	pre := scanArgs(vals, "notify-pre")
	gcStress()
	if n.keep == nil || n.tag != "nf" {
		ck(false, fmt.Sprintf("notify: receiver corrupted kind=%s seed=%d", kind, uafSeed))
	}
	post := scanArgs(vals, "notify-post")
	if pre != post {
		ck(false, fmt.Sprintf("notify: variadic slice changed across GC kind=%s pre=%d post=%d seed=%d", kind, pre, post, uafSeed))
	}
	return int64(len(kind))*1000 + post
}

func makeVals(base int64) []any {
	return []any{
		mkItem(base),
		pair{p: mkItem(base + 1), s: "pair"},
		[]*item{mkItem(base + 2), mkItem(base + 3)},
		"notify",
		int64(7) + base,
	}
}

func round(kind, r int, base int64) (path int, got, want, snap, valsSum int64, nested string) {
	tick := make(chan struct{}, 1)
	closed := make(chan struct{})
	ack := make(chan struct{}, 1)
	switch kind {
	case 0:
		tick <- struct{}{}
	case 1:
		close(closed)
	}

	var recvTick <-chan struct{} = tick
	var recvClosed <-chan struct{} = closed
	var sendAck chan struct{} = ack
	switch kind {
	case 0:
		recvClosed, sendAck = nil, nil
	case 1:
		recvTick, sendAck = nil, nil
	case 2:
		recvTick, recvClosed = nil, nil
	case 3:
		recvTick, recvClosed, sendAck = nil, nil, nil
	}

	vals := makeVals(base)
	valsSum = scanArgs(vals, "round-vals")
	defer func() {
		gcStress()
		snap = scanArgs(vals, "defer-snap")
	}()

	nf := notifier(&impl{keep: mkItem(base + 50), tag: "nf"})
	kindName := ""
	path = -1

	select {
	case <-recvTick:
		path = 0
		kindName = "tick"
	case <-recvClosed:
		path = 1
		select {
		case ack <- struct{}{}:
			nested = "sent"
		default:
			nested = "default"
		}
		if nested == "sent" {
			kindName = "closed"
		} else {
			kindName = "closed-default"
		}
	case sendAck <- struct{}{}:
		path = 2
		kindName = "ack"
	default:
		path = 3
		kindName = "default"
	}

	got = nf.Notify(kindName, vals...)
	want = int64(len(kindName))*1000 + valsSum

	switch kind {
	case 0:
		select {
		case <-tick:
			ck(false, fmt.Sprintf("round %d: tick not consumed seed=%d", r, uafSeed))
		default:
		}
	case 1:
		select {
		case <-ack:
		default:
			ck(false, fmt.Sprintf("round %d: nested ack send missing seed=%d", r, uafSeed))
		}
		select {
		case <-closed:
		default:
			ck(false, fmt.Sprintf("round %d: closed channel reported open seed=%d", r, uafSeed))
		}
	case 2:
		select {
		case <-ack:
		default:
			ck(false, fmt.Sprintf("round %d: send-case token missing seed=%d", r, uafSeed))
		}
	case 3:
		if len(ack) != 0 || len(tick) != 0 {
			ck(false, fmt.Sprintf("round %d: default path used a ready channel seed=%d", r, uafSeed))
		}
	}
	return path, got, want, 0, valsSum, nested
}

func main() {
	uafSeed = seedFromEnv()
	rounds := envRounds(64, 4096)
	var hits [4]int

	for r := 0; r < rounds; r++ {
		kind := r % 4
		base := int64(r*1000) + uafSeed%83
		path, got, want, snap, valsSum, nested := round(kind, r, base)
		gcStress()

		if path != kind {
			ck(false, fmt.Sprintf("round %d kind %d: select took path %d seed=%d", r, kind, path, uafSeed))
		}
		if got != want {
			ck(false, fmt.Sprintf("round %d kind %d: notify sum=%d want=%d seed=%d", r, kind, got, want, uafSeed))
		}
		if snap != valsSum {
			ck(false, fmt.Sprintf("round %d kind %d: defer snapshot=%d want=%d seed=%d", r, kind, snap, valsSum, uafSeed))
		}
		if kind == 1 && nested != "sent" {
			ck(false, fmt.Sprintf("round %d: nested select=%q want=sent seed=%d", r, nested, uafSeed))
		}
		if kind != 1 && nested != "" {
			ck(false, fmt.Sprintf("round %d: nested outcome=%q without closed path seed=%d", r, nested, uafSeed))
		}
		if kind < 4 {
			hits[kind]++
		}
	}

	for k := 0; k < 4; k++ {
		want := rounds / 4
		if k < rounds%4 {
			want++
		}
		if hits[k] != want {
			ck(false, fmt.Sprintf("path counters: hits[%d]=%d want=%d seed=%d", k, hits[k], want, uafSeed))
		}
	}
	if rounds >= 4 && (hits[0] == 0 || hits[1] == 0 || hits[2] == 0 || hits[3] == 0) {
		ck(false, fmt.Sprintf("not all select paths exercised seed=%d", uafSeed))
	}

	finish("l17_select_struct_iface_notify")
}
