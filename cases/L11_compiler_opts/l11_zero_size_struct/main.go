// L11: 零大小 struct——空结构体、零长数组、[]struct{}、map[string]struct{}
// 与 chan struct{} 的 len/cap/遍历/相等语义必须稳定，GC 后不丢失计数。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"unsafe"
)

var (
	failed int32
	gcSink [][]byte
	seed   uint64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(255 - i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func loadSeed() uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 1
	}
	return n
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks, seed=%d)\n", name, n, seed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

type zsEmpty struct{}

func (zsEmpty) Kind() string     { return "empty" }
func (*zsEmpty) PtrKind() string { return "ptr-empty" }

type zsOther struct{}

type zsEmbed struct {
	zsEmpty
	zsOther
	v int64
}

func (z zsEmbed) Value() int64 { return z.v }

func expectClosedSendPanic(name string, fn func()) {
	defer func() {
		r := recover()
		if r == nil {
			ck(false, name+": expected panic")
			return
		}
		if !strings.Contains(fmt.Sprint(r), "send on closed channel") {
			ck(false, name+": unexpected panic text")
		}
	}()
	fn()
}

func main() {
	seed = loadSeed()
	n := 300 + int(seed%100)

	// 类型大小与零长度数组。
	ck(unsafe.Sizeof(zsEmpty{}) == 0, "empty struct size mismatch")
	ck(unsafe.Sizeof([16]zsEmpty{}) == 0, "zero-size array size mismatch")
	ck(unsafe.Sizeof(struct{ a, b zsEmpty }{}) == 0, "all-zero-size struct size mismatch")
	ck(unsafe.Sizeof([0]int64{}) == 0, "zero-length int array size mismatch")
	ck(unsafe.Sizeof([0]string{}) == 0, "zero-length string array size mismatch")

	// 方法与接口分派。
	var e zsEmpty
	ck(e.Kind() == "empty", "empty struct value method mismatch")
	ck(e.PtrKind() == "ptr-empty", "empty struct pointer method mismatch")
	var ki interface{ Kind() string } = zsEmpty{}
	ck(ki.Kind() == "empty", "empty struct interface dispatch mismatch")
	var pi interface{ PtrKind() string } = &e
	ck(pi.PtrKind() == "ptr-empty", "empty struct pointer interface dispatch mismatch")
	emb := zsEmbed{v: 42}
	ck(emb.Kind() == "empty", "promoted zero-size method mismatch")
	ck(emb.Value() == 42, "embedding zero-size struct value mismatch")
	var vi interface{ Value() int64 } = emb
	ck(vi.Value() == 42, "embedded struct interface dispatch mismatch")

	// 零大小切片：len/cap/追加/拷贝/子切片。
	s := make([]zsEmpty, 0, n)
	for i := 0; i < n; i++ {
		s = append(s, zsEmpty{})
	}
	ck(len(s) == n, "zero-size slice length mismatch")
	ck(cap(s) >= n, "zero-size slice capacity mismatch")
	s[n-1] = zsEmpty{}
	cnt := 0
	for range s {
		cnt++
	}
	ck(cnt == n, "zero-size slice range count mismatch")
	s2 := make([]zsEmpty, n)
	ck(copy(s2, s) == n, "zero-size slice copy count mismatch")
	ck(len(s2) == n, "zero-size slice copy length mismatch")
	sub := s2[n/4 : 3*n/4]
	ck(len(sub) == 3*n/4-n/4, "zero-size subslice length mismatch")
	p := &s2[0]
	ck(*p == zsEmpty{}, "zero-size element pointer dereference mismatch")
	s3 := append(s2[:0], zsEmpty{}, zsEmpty{})
	ck(len(s3) == 2, "zero-size append length mismatch")

	var zarr [0]zsEmpty
	ck(len(zarr) == 0, "zero-size array length mismatch")
	zcnt := 0
	for range zarr {
		zcnt++
	}
	ck(zcnt == 0, "zero-size array range count mismatch")
	var zfuncs [0]func()
	ck(len(zfuncs) == 0, "zero-length func array mismatch")

	// map[string]struct{} 集合语义。
	set := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		set["k"+strconv.Itoa(i)] = struct{}{}
	}
	ck(len(set) == n, "zero-size value map length mismatch")
	if _, ok := set["k0"]; !ok {
		ck(false, "zero-size value map lookup missing")
	}
	if _, ok := set["missing"]; ok {
		ck(false, "zero-size value map lookup phantom key")
	}
	for i := 0; i < n; i += 2 {
		delete(set, "k"+strconv.Itoa(i))
	}
	ck(len(set) == n/2, "zero-size value map delete length mismatch")
	rangeCnt := 0
	for range set {
		rangeCnt++
	}
	ck(rangeCnt == n/2, "zero-size value map range count mismatch")

	zset := map[zsEmpty]int64{}
	zset[zsEmpty{}] = 7
	ck(zset[zsEmpty{}] == 7, "zero-size map key lookup mismatch")
	zv, ok := zset[zsEmpty{}]
	ck(ok && zv == 7, "zero-size map key comma-ok mismatch")

	// chan struct{}：缓冲、len、关闭后读取。
	ch := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		ch <- struct{}{}
	}
	ck(len(ch) == n && cap(ch) == n, "zero-size channel len/cap mismatch")
	gcStress()
	chCnt := 0
	for i := 0; i < n; i++ {
		<-ch
		chCnt++
	}
	ck(chCnt == n && len(ch) == 0, "zero-size channel drain mismatch")
	close(ch)
	if _, ok := <-ch; ok {
		ck(false, "closed zero-size channel returned a value")
	}
	expectClosedSendPanic("send-on-closed", func() { ch <- struct{}{} })

	// chan *zsEmpty：指针对象跨 GC。
	pch := make(chan *zsEmpty, 8)
	for i := 0; i < 8; i++ {
		pch <- &zsEmpty{}
	}
	gcStress()
	for i := 0; i < 8; i++ {
		pp := <-pch
		ck(pp != nil && *pp == zsEmpty{}, "zero-size pointer through channel corrupted")
	}

	// 接口相等性：相同零大小类型相等，不同类型不等。
	var a any = zsEmpty{}
	var b any = zsEmpty{}
	var c any = zsOther{}
	ck(a == b, "same zero-size dynamic type interface equality mismatch")
	ck(a != c, "different zero-size dynamic type interface equality mismatch")

	// 保留数据跨最终 GC。
	gcStress()
	ck(len(s2) == n, "retained zero-size slice length corrupted after GC")
	ck(len(s3) == 2, "retained append slice length corrupted after GC")
	ck(len(set) == n/2, "retained zero-size map corrupted after GC")
	ck(zset[zsEmpty{}] == 7, "retained zero-size map key corrupted after GC")
	ck(emb.Value() == 42 && emb.Kind() == "empty", "retained embedded struct corrupted after GC")
	ck(a == b && a != c, "retained interface equality corrupted after GC")

	finish("l11_zero_size_struct")
}
