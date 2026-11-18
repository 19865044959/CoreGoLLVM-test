// L18: 老环形链表节点上的 string / []byte / interface 字段热替换。
// 每轮 noinline setter 把新分配的年轻字符串、字节切片与接口对象写入老节点；
// 部分节点同时清空这三类字段，GC 后按种子谓词逐节点校验内容与零化。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	ssN       = 24
	ssRounds  = 8
	ssMaxRnds = 32
	ssDataN   = 20
)

type ssTag struct {
	id  int64
	ver int
	s   string
}

type ssNode struct {
	id   int64
	name string
	blob []byte
	tag  interface{}
	next *ssNode
}

var (
	failed int32
	gcSink [][]byte
	ssRoot *ssNode
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
	if n > ssMaxRnds {
		n = ssMaxRnds
	}
	return n
}

func ssSalt(seed int64) int {
	s := int(seed % 251)
	if s < 0 {
		s = -s
	}
	return s
}

func ssByte(id int64, ver, i, salt int) byte {
	return byte((int(id%251)*29 + ver*11 + i*7 + salt) % 251)
}

func ssCleared(id int64, ver int, seed int64) bool {
	s := seed % 1000
	if s < 0 {
		s = -s
	}
	return (int(id)+ver+int(s))%5 == 0
}

func ssName(id int64, ver int, seed int64) string {
	s := seed % 1000
	if s < 0 {
		s = -s
	}
	n := 8 + int((uint64(id)*7+uint64(ver)*3+uint64(s))%9)
	ch := byte('a' + (uint64(id)+uint64(ver)+uint64(s))%26)
	return strings.Repeat(string(rune(ch)), n) + strconv.Itoa(ver)
}

func ssBlob(id int64, ver int, seed int64) []byte {
	salt := ssSalt(seed)
	n := 12 + int((uint64(id)*3+uint64(ver)*5+uint64(ssSalt(seed)))%7)
	b := make([]byte, n)
	for i := range b {
		b[i] = ssByte(id, ver, i, salt)
	}
	return b
}

//go:noinline
func ssSetNode(n *ssNode, ver int, seed int64) {
	if ssCleared(n.id, ver, seed) {
		n.name = ""
		n.blob = nil
		n.tag = nil
		return
	}
	n.name = ssName(n.id, ver, seed)
	n.blob = ssBlob(n.id, ver, seed)
	n.tag = &ssTag{id: n.id*10 + int64(ver), ver: ver, s: n.name}
}

//go:noinline
func ssRewire(head *ssNode, ver int, seed int64) {
	cur := head
	for i := 0; i < ssN; i++ {
		ssSetNode(cur, ver, seed)
		cur = cur.next
	}
}

func ssBlobOK(b []byte, id int64, ver int, seed int64) bool {
	if len(b) != 12+int((uint64(id)*3+uint64(ver)*5+uint64(ssSalt(seed)))%7) {
		return false
	}
	salt := ssSalt(seed)
	for i := range b {
		if b[i] != ssByte(id, ver, i, salt) {
			return false
		}
	}
	return true
}

func ssCheckRing(head *ssNode, ver int, seed int64) bool {
	cur := head
	for i := 0; i < ssN; i++ {
		if cur == nil || cur.id != int64(i) {
			ck(false, fmt.Sprintf("ring broken at %d (seed=%d)", i, seed))
			return false
		}
		if ssCleared(cur.id, ver, seed) {
			if cur.name != "" || cur.blob != nil || cur.tag != nil {
				ck(false, fmt.Sprintf("node %d not cleared (seed=%d)", i, seed))
				return false
			}
		} else {
			if cur.name != ssName(cur.id, ver, seed) {
				ck(false, fmt.Sprintf("node %d name corrupted (seed=%d)", i, seed))
				return false
			}
			if !ssBlobOK(cur.blob, cur.id, ver, seed) {
				ck(false, fmt.Sprintf("node %d blob corrupted (seed=%d)", i, seed))
				return false
			}
			t, ok := cur.tag.(*ssTag)
			if !ok || t.id != cur.id*10+int64(ver) || t.ver != ver || t.s != cur.name {
				ck(false, fmt.Sprintf("node %d interface tag corrupted (seed=%d)", i, seed))
				return false
			}
		}
		cur = cur.next
	}
	if cur != head {
		ck(false, fmt.Sprintf("ring not closed (seed=%d)", seed))
		return false
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(ssRounds)

	nodes := make([]*ssNode, ssN)
	for i := range nodes {
		nodes[i] = &ssNode{id: int64(i)}
		ssSetNode(nodes[i], 1, seed)
		if i > 0 {
			nodes[i-1].next = nodes[i]
		}
	}
	nodes[ssN-1].next = nodes[0]
	ssRoot = nodes[0]
	gcStress()
	gcStress() // 整环晋升为老对象

	if !ssCheckRing(ssRoot, 1, seed) {
		finish("l18_old_string_field_swap")
		return
	}

	for r := 2; r <= rounds+1; r++ {
		ssRewire(ssRoot, r, seed)
		gcStress()
		if !ssCheckRing(ssRoot, r, seed) {
			break
		}
	}

	finish("l18_old_string_field_swap")
}
