// L03: atomic.Value 快照发布/读取循环。单写者每轮构造不可变快照并用
// Store 发布，读者循环 Load、校验 ver/node/sum/pad 自洽；每 256 次读在
// 持有旧快照时显式 GC，随后继续解引用旧快照的 node，写者此时已发布新版本
// 并可能使旧快照变成垃圾。写者自身每 512 版也显式 GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

var (
	asFailed int32
	asPub    atomic.Value
)

const (
	asTotal      = 12000
	asReads      = 24000
	asWriteGCDiv = 512
	asReadGCDiv  = 256
)

type asNode struct {
	v   int64
	tag string
}

type asSnap struct {
	ver  int64
	node *asNode
	sum  int64
	tag  string
	pad  [8]int64
}

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&asFailed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func finish(name string) {
	if n := atomic.LoadInt32(&asFailed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func seedFromEnv() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			return v
		}
	}
	return 1
}

func asCheck(s *asSnap, off int64) bool {
	return s != nil && s.node != nil && s.tag == "snap" &&
		s.node.tag == "asnode" && s.ver >= 0 &&
		s.node.v == s.ver*5+off && s.sum == s.node.v*3+s.ver &&
		s.pad[0] == s.ver
}

//go:noinline
func asWriter(done chan struct{}, off int64) {
	defer close(done)
	for v := int64(1); v <= asTotal; v++ {
		nd := &asNode{v: v*5 + off, tag: "asnode"}
		s := &asSnap{ver: v, node: nd, sum: nd.v*3 + v, tag: "snap"}
		s.pad[0] = v
		asPub.Store(s)
		if v%asWriteGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(v)
			runtime.GC()
			runtime.KeepAlive(junk)
		}
	}
}

func main() {
	seed := seedFromEnv()
	if seed < 0 {
		seed = -seed
	}
	off := seed % 17

	initNode := &asNode{v: off, tag: "asnode"}
	initSnap := &asSnap{ver: 0, node: initNode, sum: initNode.v * 3, tag: "snap"}
	asPub.Store(initSnap)

	done := make(chan struct{})
	go asWriter(done, off)

	var lastVer int64 = -1
	var bad, reads, gcs int64
	for i := 0; i < asReads; i++ {
		x := asPub.Load()
		s, ok := x.(*asSnap)
		if !ok || !asCheck(s, off) {
			bad++
		} else if s.ver < lastVer {
			bad++
		} else {
			lastVer = s.ver
		}
		if i%asReadGCDiv == 0 {
			junk := make([]byte, 16384)
			junk[0] = byte(i)
			runtime.GC()
			gcs++
			// 旧快照可能已被写者新版本取代，仍必须可安全解引用。
			if s != nil && s.node != nil {
				want := s.ver*5 + off
				if s.node.v != want || s.node.tag != "asnode" ||
					s.sum != want*3+s.ver || s.pad[0] != s.ver {
					bad++
				}
			}
			runtime.KeepAlive(s)
			runtime.KeepAlive(junk)
		}
		reads++
	}
	<-done

	final, ok := asPub.Load().(*asSnap)
	ck(ok && asCheck(final, off), fmt.Sprintf("final snapshot valid (seed=%d)", seed))
	ck(final != nil && final.ver == asTotal,
		fmt.Sprintf("writer published all versions (seed=%d)", seed))
	ck(bad == 0, fmt.Sprintf("atomic.Value snapshots consistent across GC (seed=%d)", seed))
	ck(reads == asReads, fmt.Sprintf("reader iteration count (seed=%d)", seed))
	ck(gcs == (asReads+asReadGCDiv-1)/asReadGCDiv,
		fmt.Sprintf("reader explicit GC count (seed=%d)", seed))
	ck(lastVer >= 0 && lastVer <= asTotal,
		fmt.Sprintf("version sequence monotonic (seed=%d)", seed))
	runtime.KeepAlive(initSnap)
	runtime.KeepAlive(final)
	finish("l03_atomic_value_snapshot")
}
