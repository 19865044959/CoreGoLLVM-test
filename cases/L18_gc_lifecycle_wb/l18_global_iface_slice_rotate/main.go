// L18: 全局老 []interface{} 与 map[string]interface{} 的分代轮转。
// 每轮 noinline setter 把半段 slice 与轮转 key 子集覆写为年轻值，动态类型
// 随代次切换（*node / string / []byte）；GC 后逐槽位与逐 key 校验。
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
	giN       = 96
	giMapN    = 64
	giRounds  = 6
	giMaxRnds = 30
	giDataN   = 16
)

type giNode struct {
	id   int64
	ver  int
	name string
	data [giDataN]byte
}

type giHolder struct {
	s []interface{}
	m map[string]interface{}
}

var (
	failed  int32
	gcSink  [][]byte
	giH     giHolder
	giKeys  []string
	giVert  []int
	giMVert []int
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
	if n > giMaxRnds {
		n = giMaxRnds
	}
	return n
}

func giKey(k int) string { return fmt.Sprintf("k%02d", k) }

func giSalt(seed int64) int {
	s := int(seed % 239)
	if s < 0 {
		s = -s
	}
	return s
}

func giByte(id int64, ver, i, salt int) byte {
	return byte((int(id%251)*13 + ver*37 + i*23 + salt) % 251)
}

func giName(i, ver int) string {
	return strings.Repeat(string(rune('a'+(i+ver)%26)), 6+(i+ver)%7) + strconv.Itoa(ver)
}

func giVal(i, ver int, seed int64) interface{} {
	salt := giSalt(seed)
	switch (i + ver) % 3 {
	case 0:
		id := int64(ver*10000 + i)
		n := &giNode{id: id, ver: ver, name: giName(i, ver)}
		for j := range n.data {
			n.data[j] = giByte(id, ver, j, salt)
		}
		return n
	case 1:
		return giName(i, ver)
	default:
		b := make([]byte, 16+i%13)
		for j := range b {
			b[j] = giByte(int64(i), ver, j, salt)
		}
		return b
	}
}

func giMapVal(k, ver int, seed int64) interface{} {
	salt := giSalt(seed)
	switch (k + ver) % 3 {
	case 0:
		id := int64(200000 + ver*1000 + k)
		n := &giNode{id: id, ver: ver, name: giName(k, ver)}
		for j := range n.data {
			n.data[j] = giByte(id, ver, j, salt)
		}
		return n
	case 1:
		return giName(k, ver)
	default:
		b := make([]byte, 12+k%11)
		for j := range b {
			b[j] = giByte(int64(300000+k), ver, j, salt)
		}
		return b
	}
}

//go:noinline
func giFillSegment(s []interface{}, start, end, ver int, seed int64) {
	for i := start; i < end; i++ {
		s[i] = giVal(i, ver, seed)
	}
}

//go:noinline
func giFillMap(m map[string]interface{}, keys []string, ver int, seed int64) {
	for _, k := range keys {
		var idx int
		fmt.Sscanf(k, "k%d", &idx)
		m[k] = giMapVal(idx, ver, seed)
	}
}

func giCheckElem(v interface{}, i, ver int, seed int64) bool {
	salt := giSalt(seed)
	switch (i + ver) % 3 {
	case 0:
		id := int64(ver*10000 + i)
		n, ok := v.(*giNode)
		if !ok || n.id != id || n.ver != ver || n.name != giName(i, ver) {
			return false
		}
		for j := range n.data {
			if n.data[j] != giByte(id, ver, j, salt) {
				return false
			}
		}
	case 1:
		s, ok := v.(string)
		if !ok || s != giName(i, ver) {
			return false
		}
	default:
		b, ok := v.([]byte)
		if !ok || len(b) != 16+i%13 {
			return false
		}
		for j := range b {
			if b[j] != giByte(int64(i), ver, j, salt) {
				return false
			}
		}
	}
	return true
}

func giCheckMapVal(v interface{}, k, ver int, seed int64) bool {
	salt := giSalt(seed)
	switch (k + ver) % 3 {
	case 0:
		id := int64(200000 + ver*1000 + k)
		n, ok := v.(*giNode)
		if !ok || n.id != id || n.ver != ver || n.name != giName(k, ver) {
			return false
		}
		for j := range n.data {
			if n.data[j] != giByte(id, ver, j, salt) {
				return false
			}
		}
	case 1:
		s, ok := v.(string)
		if !ok || s != giName(k, ver) {
			return false
		}
	default:
		b, ok := v.([]byte)
		if !ok || len(b) != 12+k%11 {
			return false
		}
		for j := range b {
			if b[j] != giByte(int64(300000+k), ver, j, salt) {
				return false
			}
		}
	}
	return true
}

func main() {
	seed := seedValue()
	rounds := tortureRounds(giRounds)

	giH.s = make([]interface{}, giN)
	giH.m = make(map[string]interface{}, giMapN)
	giKeys = make([]string, giMapN)
	giVert = make([]int, giN)
	giMVert = make([]int, giMapN)
	for k := 0; k < giMapN; k++ {
		giKeys[k] = giKey(k)
		giMVert[k] = 1
	}
	giFillSegment(giH.s, 0, giN, 1, seed)
	for k := 0; k < giMapN; k++ {
		giH.m[giKeys[k]] = giMapVal(k, 1, seed)
	}
	for i := range giVert {
		giVert[i] = 1
	}
	gcStress()
	gcStress() // 老 slice 底层数组与 map 晋升

	for r := 2; r <= rounds+1; r++ {
		seg := (r - 2) % 2
		start := seg * (giN / 2)
		end := start + giN/2
		giFillSegment(giH.s, start, end, r, seed)
		for i := start; i < end; i++ {
			giVert[i] = r
		}

		var keys []string
		for k := 0; k < giMapN; k++ {
			if k%2 == seg {
				keys = append(keys, giKeys[k])
				giMVert[k] = r
			}
		}
		giFillMap(giH.m, keys, r, seed)
		gcStress()

		if len(giH.s) != giN || len(giH.m) != giMapN {
			ck(false, fmt.Sprintf("round %d: container size broken s=%d m=%d (seed=%d)", r, len(giH.s), len(giH.m), seed))
			break
		}
		bad := false
		for i := 0; i < giN; i++ {
			if !giCheckElem(giH.s[i], i, giVert[i], seed) {
				ck(false, fmt.Sprintf("round %d: slice slot %d gen=%d corrupted (seed=%d)", r, i, giVert[i], seed))
				bad = true
				break
			}
		}
		if bad {
			break
		}
		for k := 0; k < giMapN; k++ {
			if !giCheckMapVal(giH.m[giKeys[k]], k, giMVert[k], seed) {
				ck(false, fmt.Sprintf("round %d: map key %s gen=%d corrupted (seed=%d)", r, giKeys[k], giMVert[k], seed))
				bad = true
				break
			}
		}
		if bad {
			break
		}
	}

	finish("l18_global_iface_slice_rotate")
}
