// L12: static composite table with interface values and pointer-rich
// elements. entries, byKey, byIndex, ptrs, ifaceSlice and funcs are all
// package-level composites built from linker-relocated data. init only takes a
// snapshot; the main program type-switches every interface element, checks
// pointer identity into nodes/blobs, and validates under concurrent readers
// and repeated explicit GC.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

type item struct {
	id     int
	name   string
	weight int16
	blob   *[8]byte
}

type custom struct {
	n   int
	tag string
}

type entry struct {
	key  string
	val  any
	tag  [4]byte
	ptr  *item
	blob *[8]byte
}

var blobs = [16][8]byte{
	{0, 1, 2, 3, 4, 5, 6, 7},
	{8, 9, 10, 11, 12, 13, 14, 15},
	{16, 17, 18, 19, 20, 21, 22, 23},
	{24, 25, 26, 27, 28, 29, 30, 31},
	{32, 33, 34, 35, 36, 37, 38, 39},
	{40, 41, 42, 43, 44, 45, 46, 47},
	{48, 49, 50, 51, 52, 53, 54, 55},
	{56, 57, 58, 59, 60, 61, 62, 63},
	{64, 65, 66, 67, 68, 69, 70, 71},
	{72, 73, 74, 75, 76, 77, 78, 79},
	{80, 81, 82, 83, 84, 85, 86, 87},
	{88, 89, 90, 91, 92, 93, 94, 95},
	{96, 97, 98, 99, 100, 101, 102, 103},
	{104, 105, 106, 107, 108, 109, 110, 111},
	{112, 113, 114, 115, 116, 117, 118, 119},
	{120, 121, 122, 123, 124, 125, 126, 127},
}

var nodes = [16]item{
	{0, "node-00", 0, &blobs[0]},
	{1, "node-01", 3, &blobs[1]},
	{2, "node-02", 6, &blobs[2]},
	{3, "node-03", 9, &blobs[3]},
	{4, "node-04", 12, &blobs[4]},
	{5, "node-05", 15, &blobs[5]},
	{6, "node-06", 18, &blobs[6]},
	{7, "node-07", 21, &blobs[7]},
	{8, "node-08", 24, &blobs[8]},
	{9, "node-09", 27, &blobs[9]},
	{10, "node-10", 30, &blobs[10]},
	{11, "node-11", 33, &blobs[11]},
	{12, "node-12", 36, &blobs[12]},
	{13, "node-13", 39, &blobs[13]},
	{14, "node-14", 42, &blobs[14]},
	{15, "node-15", 45, &blobs[15]},
}

var entries = [32]entry{
	{"e00", &nodes[0], [4]byte{0, 1, 2, 3}, &nodes[0], &blobs[0]},
	{"e01", &nodes[1], [4]byte{1, 2, 3, 4}, &nodes[1], &blobs[1]},
	{"e02", &nodes[2], [4]byte{2, 3, 4, 5}, &nodes[2], &blobs[2]},
	{"e03", &nodes[3], [4]byte{3, 4, 5, 6}, &nodes[3], &blobs[3]},
	{"e04", &nodes[4], [4]byte{4, 5, 6, 7}, &nodes[4], &blobs[4]},
	{"e05", &nodes[5], [4]byte{5, 6, 7, 8}, &nodes[5], &blobs[5]},
	{"e06", &nodes[6], [4]byte{6, 7, 8, 9}, &nodes[6], &blobs[6]},
	{"e07", &nodes[7], [4]byte{7, 8, 9, 10}, &nodes[7], &blobs[7]},
	{"e08", &nodes[8], [4]byte{8, 9, 10, 11}, &nodes[8], &blobs[8]},
	{"e09", &nodes[9], [4]byte{9, 10, 11, 12}, &nodes[9], &blobs[9]},
	{"e10", &nodes[10], [4]byte{10, 11, 12, 13}, &nodes[10], &blobs[10]},
	{"e11", &nodes[11], [4]byte{11, 12, 13, 14}, &nodes[11], &blobs[11]},
	{"e12", &nodes[12], [4]byte{12, 13, 14, 15}, &nodes[12], &blobs[12]},
	{"e13", &nodes[13], [4]byte{13, 14, 15, 16}, &nodes[13], &blobs[13]},
	{"e14", &nodes[14], [4]byte{14, 15, 16, 17}, &nodes[14], &blobs[14]},
	{"e15", &nodes[15], [4]byte{15, 16, 17, 18}, &nodes[15], &blobs[15]},
	{"e16", "str-16", [4]byte{16, 17, 18, 19}, &nodes[0], &blobs[0]},
	{"e17", "str-17", [4]byte{17, 18, 19, 20}, &nodes[1], &blobs[1]},
	{"e18", int64(1818), [4]byte{18, 19, 20, 21}, &nodes[2], &blobs[2]},
	{"e19", int64(1919), [4]byte{19, 20, 21, 22}, &nodes[3], &blobs[3]},
	{"e20", []byte("bytes-20"), [4]byte{20, 21, 22, 23}, &nodes[4], &blobs[4]},
	{"e21", []byte("bytes-21"), [4]byte{21, 22, 23, 24}, &nodes[5], &blobs[5]},
	{"e22", map[string]int{"x": 22, "y": 220}, [4]byte{22, 23, 24, 25}, &nodes[6], &blobs[6]},
	{"e23", map[string]int{"z": 23}, [4]byte{23, 24, 25, 26}, &nodes[7], &blobs[7]},
	{"e24", custom{n: 24, tag: "c24"}, [4]byte{24, 25, 26, 27}, &nodes[8], &blobs[8]},
	{"e25", custom{n: 25, tag: "c25"}, [4]byte{25, 26, 27, 28}, &nodes[9], &blobs[9]},
	{"e26", func(n int) int { return n + 26 }, [4]byte{26, 27, 28, 29}, &nodes[10], &blobs[10]},
	{"e27", func(n int) int { return n * 27 }, [4]byte{27, 28, 29, 30}, &nodes[11], &blobs[11]},
	{"e28", nil, [4]byte{28, 29, 30, 31}, &nodes[12], &blobs[12]},
	{"e29", &blobs[9], [4]byte{29, 30, 31, 32}, &nodes[13], &blobs[13]},
	{"e30", &nodes[15], [4]byte{30, 31, 32, 33}, &nodes[14], &blobs[14]},
	{"e31", custom{n: 31, tag: "c31"}, [4]byte{31, 32, 33, 34}, &nodes[15], &blobs[15]},
}

var byKey = map[string]*item{
	"n00": &nodes[0], "n01": &nodes[1], "n02": &nodes[2], "n03": &nodes[3],
	"n04": &nodes[4], "n05": &nodes[5], "n06": &nodes[6], "n07": &nodes[7],
	"n08": &nodes[8], "n09": &nodes[9], "n10": &nodes[10], "n11": &nodes[11],
	"n12": &nodes[12], "n13": &nodes[13], "n14": &nodes[14], "n15": &nodes[15],
}

var byIndex = map[int]any{
	0: &nodes[0],
	1: "one",
	2: int64(2),
	3: []byte("three"),
	4: custom{n: 4, tag: "c4"},
	5: func(n int) int { return n * 5 },
}

var ptrs = [16]*item{
	&nodes[0], &nodes[1], &nodes[2], &nodes[3],
	&nodes[4], &nodes[5], &nodes[6], &nodes[7],
	&nodes[8], &nodes[9], &nodes[10], &nodes[11],
	&nodes[12], &nodes[13], &nodes[14], &nodes[15],
}

var ifaceSlice = []any{
	&nodes[1], "iface-1", int64(1), []byte("iface-3"), custom{n: 4, tag: "ic4"}, nil,
}

var funcs = [4]func(int) int{
	func(n int) int { return n + 1 },
	func(n int) int { return n * 2 },
	func(n int) int { return n - 1 },
	func(n int) int { return n * n },
}

type initSnapshot struct {
	nodes   int
	entries int
	byKey   int
	first   *item
	last    *item
	probe   int
}

var snap initSnapshot

func init() {
	snap = initSnapshot{
		nodes:   len(nodes),
		entries: len(entries),
		byKey:   len(byKey),
		first:   &nodes[0],
		last:    &nodes[15],
		probe:   byKey["n03"].id,
	}
}

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress(rounds int) {
	for r := 0; r < rounds; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i + r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	gcSink = gcSink[:0]
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks) seed=%s\n", name, n, seedStr)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		return 1
	}
	return v
}

func readRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 8 {
		return 8
	}
	return v
}

func verifyOnce() int {
	bad := 0
	fail := func(msg string) {
		bad++
		if bad <= 3 {
			fmt.Println("FAIL:", msg)
		}
	}

	if snap.nodes != 16 || snap.entries != 32 || snap.byKey != 16 {
		fail("init snapshot shape")
	}
	if snap.first != &nodes[0] || snap.last != &nodes[15] || snap.probe != 3 {
		fail("init snapshot pointer identity")
	}
	if len(entries) != 32 || len(byKey) != 16 || len(byIndex) != 6 ||
		len(ptrs) != 16 || len(ifaceSlice) != 6 || len(funcs) != 4 {
		fail("static table lengths")
	}

	for i := 0; i < 16; i++ {
		n := &nodes[i]
		if n.id != i || n.name != fmt.Sprintf("node-%02d", i) || n.weight != int16(i*3) {
			fail(fmt.Sprintf("node %d fields", i))
			break
		}
		if n.blob != &blobs[i] {
			fail(fmt.Sprintf("node %d blob identity", i))
			break
		}
		for j := 0; j < 8; j++ {
			if n.blob[j] != byte(i*8+j) {
				fail(fmt.Sprintf("node %d blob byte %d", i, j))
				break
			}
		}
		if ptrs[i] != n || byKey[fmt.Sprintf("n%02d", i)] != n {
			fail(fmt.Sprintf("node %d static pointer identity", i))
			break
		}
	}

	if v, ok := byIndex[0].(*item); !ok || v != &nodes[0] {
		fail("byIndex pointer value")
	}
	if byIndex[1] != "one" || byIndex[2].(int64) != 2 {
		fail("byIndex scalar values")
	}
	if string(byIndex[3].([]byte)) != "three" {
		fail("byIndex byte slice")
	}
	if byIndex[4].(custom) != (custom{n: 4, tag: "c4"}) {
		fail("byIndex custom value")
	}
	if byIndex[5].(func(int) int)(3) != 15 {
		fail("byIndex function value")
	}

	for i := 0; i < 32; i++ {
		e := &entries[i]
		if e.key != fmt.Sprintf("e%02d", i) {
			fail(fmt.Sprintf("entry %d key", i))
			break
		}
		if e.tag != [4]byte{byte(i), byte(i + 1), byte(i + 2), byte(i + 3)} {
			fail(fmt.Sprintf("entry %d tag", i))
			break
		}
		if e.ptr != &nodes[i%16] || e.blob != &blobs[i%16] {
			fail(fmt.Sprintf("entry %d pointer fields", i))
			break
		}
	}
	for i := 0; i < 16; i++ {
		if v, ok := entries[i].val.(*item); !ok || v != &nodes[i] || v.id != i {
			fail(fmt.Sprintf("entry %d item interface", i))
			break
		}
	}
	if entries[16].val != "str-16" || entries[17].val != "str-17" {
		fail("string interface entries")
	}
	if entries[18].val.(int64) != 1818 || entries[19].val.(int64) != 1919 {
		fail("int64 interface entries")
	}
	if string(entries[20].val.([]byte)) != "bytes-20" || string(entries[21].val.([]byte)) != "bytes-21" {
		fail("byte slice interface entries")
	}
	m22 := entries[22].val.(map[string]int)
	m23 := entries[23].val.(map[string]int)
	if m22["x"] != 22 || m22["y"] != 220 || m23["z"] != 23 {
		fail("map interface entries")
	}
	if entries[24].val.(custom) != (custom{n: 24, tag: "c24"}) ||
		entries[25].val.(custom) != (custom{n: 25, tag: "c25"}) {
		fail("custom interface entries")
	}
	if entries[26].val.(func(int) int)(4) != 30 || entries[27].val.(func(int) int)(3) != 81 {
		fail("function interface entries")
	}
	if entries[28].val != nil {
		fail("nil interface entry")
	}
	if v, ok := entries[29].val.(*[8]byte); !ok || v != &blobs[9] || v[0] != 72 {
		fail("array pointer interface entry")
	}
	if v, ok := entries[30].val.(*item); !ok || v != &nodes[15] {
		fail("duplicate pointer interface entry")
	}
	if entries[31].val.(custom) != (custom{n: 31, tag: "c31"}) {
		fail("custom interface entry 31")
	}

	if ifaceSlice[0] != &nodes[1] || ifaceSlice[1] != "iface-1" || ifaceSlice[2].(int64) != 1 ||
		string(ifaceSlice[3].([]byte)) != "iface-3" || ifaceSlice[4].(custom).tag != "ic4" || ifaceSlice[5] != nil {
		fail("ifaceSlice values")
	}
	if funcs[0](3) != 4 || funcs[1](3) != 6 || funcs[2](3) != 2 || funcs[3](3) != 9 {
		fail("static function array")
	}
	return bad
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := readRounds()

	ck(verifyOnce() == 0, "static composite table initial validation")

	// Concurrent read-only validation with explicit GC.
	workers := 8
	iters := 100 * rounds
	start := int(seedVal % 32)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				idx := (start + w + i) % 32
				e := &entries[idx]
				if e.ptr != &nodes[idx%16] || e.blob != &blobs[idx%16] {
					atomic.AddInt32(&bad, 1)
					return
				}
				if byKey[fmt.Sprintf("n%02d", idx%16)] != &nodes[idx%16] {
					atomic.AddInt32(&bad, 1)
					return
				}
				if ifaceSlice[idx%6] != nil && idx%6 == 0 {
					if ifaceSlice[0] != &nodes[1] {
						atomic.AddInt32(&bad, 1)
						return
					}
				}
				if funcs[idx%4](2) == 0 {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	gcStress(3)
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent static table read failed under GC")

	// Torture rounds re-run the full structural validation.
	for r := 0; r < rounds; r++ {
		if verifyOnce() != 0 {
			ck(false, fmt.Sprintf("torture round %d validation", r))
			break
		}
		gcStress(1)
	}

	gcStress(2)
	ck(len(entries) == 32 && len(byKey) == 16 && len(byIndex) == 6 && len(ptrs) == 16 && len(ifaceSlice) == 6, "table lengths changed after GC")
	ck(entries[0].ptr == &nodes[0] && entries[16].blob == &blobs[0] && entries[29].ptr == &nodes[13], "entry identity changed after GC")
	ck(byKey["n15"] == &nodes[15] && ptrs[15] == &nodes[15], "node identity changed after GC")
	ck(entries[0].val.(*item) == &nodes[0] && string(entries[20].val.([]byte)) == "bytes-20", "interface payload changed after GC")
	ck(blobs[15][7] == 127 && nodes[15].weight == 45, "static bytes changed after GC")

	finish("l12_static_iface_gcprog")
}
