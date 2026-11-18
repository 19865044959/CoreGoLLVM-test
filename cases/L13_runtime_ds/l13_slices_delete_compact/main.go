// L13: slices.Delete/Compact/DeleteFunc/Replace/Grow 的指针元素语义与尾段复用、GC。
package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync/atomic"
)

type dcItem struct {
	key  int64
	seq  int64
	name string
	pad  []byte
}

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
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

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func newDC(seq, salt int64) *dcItem {
	key := seq % 37
	d := &dcItem{key: key, seq: seq, name: fmt.Sprintf("dc-%05d-%02d", seq, salt%61), pad: make([]byte, 7)}
	for i := range d.pad {
		d.pad[i] = byte(seq*19 + int64(i*3) + key)
	}
	return d
}

func dcValid(d *dcItem, salt int64) bool {
	if d == nil || d.key != d.seq%37 || d.name != fmt.Sprintf("dc-%05d-%02d", d.seq, salt%61) || len(d.pad) != 7 {
		return false
	}
	for i := range d.pad {
		if d.pad[i] != byte(d.seq*19+int64(i*3)+d.key) {
			return false
		}
	}
	return true
}

func ptrSeqEqualDC(a, b []*dcItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()

	items := make([]*dcItem, 1024)
	for i := range items {
		items[i] = newDC(int64(i), curSeed)
	}

	// 1) Delete：中段删除后前后段指针顺序不变。
	s := append([]*dcItem(nil), items...)
	del := slices.Delete(s, 100, 300)
	ck(len(del) == 824, "slices.Delete: len")
	left := append([]*dcItem(nil), items[:100]...)
	right := append([]*dcItem(nil), items[300:]...)
	ck(ptrSeqEqualDC(del[:100], left), "slices.Delete: prefix")
	ck(ptrSeqEqualDC(del[100:], right), "slices.Delete: suffix")

	// 2) Compact：连续重复指针只保留第一个。
	var dup []*dcItem
	var wantCP []*dcItem
	for i := 0; i < len(items); i += 3 {
		rep := 1 + (i/3)%3
		wantCP = append(wantCP, items[i])
		for j := 0; j < rep; j++ {
			dup = append(dup, items[i])
		}
	}
	cp := slices.Compact(dup)
	ck(ptrSeqEqualDC(cp, wantCP), "slices.Compact: consecutive duplicates")

	// 3) DeleteFunc：按 key/seq 谓词删除。
	df := slices.DeleteFunc(append([]*dcItem(nil), del...), func(d *dcItem) bool {
		return d.key == 17 || d.seq%13 == 0
	})
	var wantDF []*dcItem
	for _, d := range del {
		if d.key != 17 && d.seq%13 != 0 {
			wantDF = append(wantDF, d)
		}
	}
	ck(ptrSeqEqualDC(df, wantDF), "slices.DeleteFunc: survivors")

	// 4) Replace：替换中段为新的指针元素。
	base := append([]*dcItem(nil), del...)
	baseRef := append([]*dcItem(nil), base...)
	nExtra := 3 + int(curSeed%4)
	extras := make([]*dcItem, nExtra)
	for i := range extras {
		extras[i] = newDC(int64(5000+i), curSeed)
	}
	rep := slices.Replace(base, 50, 60, extras...)
	var wantRep []*dcItem
	wantRep = append(wantRep, baseRef[:50]...)
	wantRep = append(wantRep, extras...)
	wantRep = append(wantRep, baseRef[60:]...)
	ck(ptrSeqEqualDC(rep, wantRep), "slices.Replace: composition")

	// 5) Grow + append：长度不变，追加后旧前缀指针不变。
	growN := 300 + int(curSeed%100)
	g := slices.Grow(append([]*dcItem(nil), baseRef...), growN)
	ck(len(g) == len(baseRef), "slices.Grow: len changed")
	ck(ptrSeqEqualDC(g, baseRef), "slices.Grow: content changed")
	if cap(g) < len(g)+growN {
		ckSeed(false, "slices.Grow: insufficient capacity")
	}
	appended := g
	for i := 0; i < growN; i++ {
		appended = append(appended, newDC(int64(9000+i), curSeed))
	}
	ck(len(appended) == len(baseRef)+growN, "grow append: len")
	ck(ptrSeqEqualDC(appended[:len(baseRef)], baseRef), "grow append: prefix")
	for i := 0; i < growN; i++ {
		if !dcValid(appended[len(baseRef)+i], curSeed) {
			ckSeed(false, fmt.Sprintf("grow append: new element invalid at %d", i))
			break
		}
	}

	// 尾段复用：删除后追加不得复活已删除元素。
	reuse := slices.Delete(append([]*dcItem(nil), items...), 700, 724)
	const addN = 64
	for i := 0; i < addN; i++ {
		reuse = append(reuse, newDC(int64(12000+i), curSeed))
	}
	ck(len(reuse) == len(items)-24+addN, "tail reuse: len")
	ck(ptrSeqEqualDC(reuse[:700], items[:700]), "tail reuse: prefix")
	ck(ptrSeqEqualDC(reuse[700:len(items)-24], items[724:]), "tail reuse: shifted suffix")
	for i := 0; i < addN; i++ {
		if !dcValid(reuse[len(reuse)-addN+i], curSeed) {
			ckSeed(false, fmt.Sprintf("tail reuse: appended element invalid at %d", i))
			break
		}
	}

	gcStress()
	for i, d := range items {
		if !dcValid(d, curSeed) {
			ckSeed(false, fmt.Sprintf("slices: item payload corrupted at %d after GC", i))
			break
		}
	}
	ck(ptrSeqEqualDC(reuse[:700], items[:700]), "slices: reuse prefix after GC")
	for _, d := range extras {
		if !dcValid(d, curSeed) {
			ckSeed(false, "slices: extras payload corrupted after GC")
			break
		}
	}
	gcStress()

	runtime.KeepAlive(items)
	runtime.KeepAlive(del)
	runtime.KeepAlive(cp)
	runtime.KeepAlive(df)
	runtime.KeepAlive(rep)
	runtime.KeepAlive(appended)
	runtime.KeepAlive(reuse)
	finish("l13_slices_delete_compact")
}
