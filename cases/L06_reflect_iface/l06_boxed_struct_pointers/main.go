// L06: 含指针字段的 struct 值装箱进接口，GC 后按值断言并继续解引用。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type node struct {
	V     int64
	Label string
	Next  *node
}

type blob struct {
	Data [64]byte
}

type payload struct {
	ID    int64
	Name  string
	Next  *node
	Blob  *blob
	Count *int64
}

func (p payload) String() string { return p.Name }

type describer interface{ Describe() string }

func (p *payload) Describe() string { return p.Name }

var (
	failed  int32
	gcSink  [][]byte
	boxSink any
	uafSeed int64 = 1
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
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func buildChain(n int) (*node, int64, int) {
	head := &node{V: 0, Label: "h0"}
	cur := head
	var sum int64
	for i := 1; i <= n; i++ {
		nx := &node{V: int64(i), Label: fmt.Sprintf("n%d", i)}
		cur.Next = nx
		cur = nx
		sum += int64(i)
	}
	return head, sum, n + 1
}

func main() {
	uafSeed = envSeed()
	const rounds = 12
	for r := 0; r < rounds; r++ {
		n1 := &node{V: 11, Label: "n1"}
		n2 := &node{V: 12, Label: "n2"}
		n1.Next = n2
		bl := &blob{}
		for i := 0; i < len(bl.Data); i++ {
			bl.Data[i] = byte(i)
		}
		cnt := int64(77)

		p := payload{ID: 1, Name: "boxed", Next: n1, Blob: bl, Count: &cnt}
		var box any = p
		gcStress()

		got, ok := box.(payload)
		ck(ok, "payload value assertion")
		ck(got.ID == 1 && got.Name == "boxed", "boxed struct scalar fields")
		ck(got.Next != nil && got.Next.V == 11 && got.Next.Next.V == 12, "boxed struct pointer chain")
		ck(got.Blob != nil && got.Blob.Data[0] == 0 && got.Blob.Data[63] == 63, "boxed struct array pointer")
		ck(got.Count != nil && *got.Count == 77, "boxed struct scalar pointer")

		// 值拷贝独立，指针字段共享。
		n1.V = 111
		bl.Data[0] = 200
		cnt = 88
		p.Name = "local-changed"
		gcStress()
		got2 := box.(payload)
		ck(got2.Name == "boxed", "struct value copied into interface")
		ck(got2.Next.V == 111, "boxed value shares next pointer")
		ck(got2.Blob.Data[0] == 200, "boxed value shares blob pointer")
		ck(*got2.Count == 88, "boxed value shares count pointer")

		var s fmt.Stringer = p
		var dp describer = &p
		gcStress()
		ck(s.String() == "local-changed", "boxed value implements Stringer")
		ck(dp.Describe() == "local-changed", "boxed pointer implements describer")

		// 只把链表头节点值装箱进全局接口，GC 后靠 Next 指针走完整条链。
		length := 8 + int(uafSeed%5)
		head, wantSum, wantCount := buildChain(length)
		boxSink = *head
		head = nil
		gcStress()
		gcStress()

		first, ok := boxSink.(node)
		ck(ok, "boxed node head assertion")
		var walked int64
		count := 0
		last := ""
		for cur := &first; cur != nil; cur = cur.Next {
			walked += cur.V
			count++
			last = cur.Label
		}
		ck(count == wantCount, "boxed head chain length")
		ck(walked == wantSum, "boxed head chain values survive GC")
		ck(last == fmt.Sprintf("n%d", length), "boxed head chain tail label")

		// 通过装箱副本的 Next 指针修改原链表节点。
		first.Next.V = 4242
		ck(head == nil, "original head dropped")
		ck(first.Next.V == 4242 && first.Next.Label == "n1", "write through boxed copy pointer")
	}

	finish("l06_boxed_struct_pointers")
}
