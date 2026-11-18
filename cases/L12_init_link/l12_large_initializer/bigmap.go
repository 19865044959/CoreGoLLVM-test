package main

import (
	"os"
	"strconv"
)

type bigItem struct {
	id      int
	label   string
	payload []byte
	next    *bigItem
}

// envRounds 读取 TORTURE_ROUNDS（默认 1，上限 20）。
func envRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 20 {
		v = 20
	}
	return v
}

// bigN 在包级变量初始化阶段按环境变量决定 map 规模。
func bigN() int {
	n := 12000 * envRounds()
	if n > 240000 {
		n = 240000
	}
	return n
}

func newBigItem(i int) *bigItem {
	p := make([]byte, 24)
	for j := range p {
		p[j] = byte(i*7 + j)
	}
	return &bigItem{id: i, label: "item", payload: p}
}

func buildBigMap(n int) (map[int]*bigItem, int64, *bigItem) {
	m := make(map[int]*bigItem, n)
	var sum int64
	var prev *bigItem
	for i := 0; i < n; i++ {
		it := newBigItem(i)
		it.next = prev
		prev = it
		m[i] = it
		sum += int64(i)
	}
	return m, sum, prev
}

func makeBigBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + 3)
	}
	return b
}

var (
	bigMapCount                     = bigN()
	bigMap, bigMapIDSum, bigMapHead = buildBigMap(bigMapCount)
	bigBytes                        = makeBigBytes(16384)
	bigInitBad                      bool
)

func init() {
	// init 阶段即可读取完整的包级大对象。
	if len(bigMap) != bigMapCount || bigMapHead == nil || bigMapHead.id != bigMapCount-1 {
		bigInitBad = true
		return
	}
	if bigMap[0] == nil || bigMap[bigMapCount-1] == nil {
		bigInitBad = true
	}
}
