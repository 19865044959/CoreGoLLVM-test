// L12: 纯静态复合字面量表。nodes/byName/byID/groups/anon 全部在包级复合
// 字面量中定义，不经过 init 构造；init 只抓取快照。用例验证链接期数据
// 重定位、指针字段的 gcprog 扫描，以及多轮显式 GC 后表与指针身份不变。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	initNodesLen   int
	initLookupOK   bool
	initSnapshotOK bool
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

func nodeName(i int) string { return fmt.Sprintf("n%03d", i) }

func tortureRounds() int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return 1
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return 1
	}
	if v > 16 {
		return 16
	}
	return v
}

func init() {
	initNodesLen = len(nodes)
	initLookupOK = byName[nodeName(0)] == nodes[0] && byID[95] == nodes[95]
	initSnapshotOK = initNodesLen == 96 &&
		len(byName) == 96 && len(byID) == 96 &&
		len(groups) == 8 && len(groups["g7"]) == 12 && len(anon) == 24
}

// validateAll returns a non-empty deterministic description of the first
// violated invariant, or "" when every static table is intact.
func validateAll() string {
	if len(nodes) != 96 {
		return fmt.Sprintf("nodes length %d", len(nodes))
	}
	if len(byName) != 96 || len(byID) != 96 || len(groups) != 8 || len(anon) != 24 {
		return fmt.Sprintf("table lengths byName=%d byID=%d groups=%d anon=%d",
			len(byName), len(byID), len(groups), len(anon))
	}
	for i, it := range nodes {
		if it == nil {
			return fmt.Sprintf("node %d nil", i)
		}
		if it.id != i || it.name != nodeName(i) || it.weight != int16(100+i*2) {
			return fmt.Sprintf("node %d fields id=%d name=%q weight=%d", i, it.id, it.name, it.weight)
		}
		for j := 0; j < 4; j++ {
			if it.tag[j] != byte((i*3+j)&0xff) {
				return fmt.Sprintf("node %d tag[%d]", i, j)
			}
		}
		if it.payload == nil {
			return fmt.Sprintf("node %d payload nil", i)
		}
		for j := 0; j < 4; j++ {
			if it.payload.data[j] != byte((i*5+j)&0xff) {
				return fmt.Sprintf("node %d payload[%d]", i, j)
			}
		}
		if byName[nodeName(i)] != it || byID[i] != it {
			return fmt.Sprintf("node %d map identity", i)
		}
		g := groups[fmt.Sprintf("g%d", i%8)]
		if len(g) != 12 || g[i/8] != it {
			return fmt.Sprintf("node %d group membership", i)
		}
	}
	for k := 0; k < len(anon); k++ {
		wantIdx := (k * 11) % 96
		if anon[k].it != nodes[wantIdx] || anon[k].weight != int16(1000+k) || anon[k].label != fmt.Sprintf("a%02d", k) {
			return fmt.Sprintf("anon entry %d", k)
		}
	}
	return ""
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)
	rounds := tortureRounds()

	ck(initSnapshotOK, "init snapshot saw incomplete static tables")
	ck(initNodesLen == 96, "init-observed nodes length")
	ck(initLookupOK, "init-observed static map identity")
	ck(validateAll() == "", "static tables invalid before GC")

	// Concurrent readers of the static tables while main allocates garbage and
	// triggers explicit GC.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := int(seedVal % 96)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 120; i++ {
				idx := (start + w*5 + i) % 96
				it := nodes[idx]
				if it == nil || it.id != idx || byID[idx] != it || byName[nodeName(idx)] != it {
					atomic.AddInt32(&bad, 1)
					return
				}
				if groups[fmt.Sprintf("g%d", idx%8)][idx/8] != it {
					atomic.AddInt32(&bad, 1)
					return
				}
				k := (w*3 + i) % len(anon)
				if anon[k].it != nodes[(k*11)%96] {
					atomic.AddInt32(&bad, 1)
					return
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent static table read failed under GC")

	// TORTURE_ROUNDS scales repeated full-table verification plus explicit GC.
	for r := 0; r < rounds; r++ {
		if msg := validateAll(); msg != "" {
			ck(false, fmt.Sprintf("torture round %d: %s", r, msg))
			break
		}
		ck(initNodesLen == 96 && initSnapshotOK && initLookupOK, "init snapshot changed")
		gcStress()
	}

	gcStress()
	ck(validateAll() == "", "static tables invalid after GC")
	ck(nodes[0] == byName["n000"], "pointer identity lost after GC")
	ck(nodes[95] == byID[95], "last pointer identity lost after GC")
	ck(len(anon) == 24 && anon[23].it == nodes[(23*11)%96], "anon relocations lost after GC")

	finish("l12_static_composite_table")
}
