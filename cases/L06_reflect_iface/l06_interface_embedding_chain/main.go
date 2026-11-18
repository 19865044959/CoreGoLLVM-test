// L06: 接口嵌入、多层 wrapper、方法集与提升方法，GC 后动态派发正确。
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
}

type reader interface{ Read() int64 }
type writer interface{ Write(int64) }
type closer interface{ Close() int64 }

type readWriter interface {
	reader
	writer
}

type readWriteCloser interface {
	readWriter
	closer
}

type labeler interface{ Label() string }

type mega interface {
	readWriteCloser
	labeler
}

type device struct {
	buf    int64
	tag    *node
	id     int64
	closed bool
}

func (d *device) Read() int64   { return d.buf + d.tag.V }
func (d *device) Write(v int64) { d.buf = v }
func (d *device) Close() int64  { d.closed = true; return d.id*1000 + d.buf + d.tag.V }
func (d *device) Label() string { return d.tag.Label }

// promoted 通过匿名嵌入提升 *device 的全部方法。
type promoted struct{ *device }

type wrap struct {
	inner readWriteCloser
	tag   *node
}

func (w *wrap) Read() int64   { gcStress(); return w.inner.Read() + w.tag.V }
func (w *wrap) Write(v int64) { w.inner.Write(v) }
func (w *wrap) Close() int64  { return w.inner.Close() }

func chainRW(inner readWriteCloser, depth int) readWriteCloser {
	for i := 0; i < depth; i++ {
		inner = &wrap{inner: inner, tag: &node{V: 1, Label: "w"}}
	}
	return inner
}

// vadd 使用值接收者，值与指针都实现 reader。
type vadd struct {
	inner reader
	add   int64
}

func (v vadd) Read() int64 { return v.inner.Read() + v.add }

var _ readWriteCloser = (*promoted)(nil)
var _ readWriteCloser = promoted{}
var _ mega = (*promoted)(nil)
var _ reader = vadd{}
var _ reader = (*vadd)(nil)

var (
	failed    int32
	gcSink    [][]byte
	ifaceSink []any
	uafSeed   int64 = 1
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

type spacer interface{ Space() int64 }

func main() {
	uafSeed = envSeed()
	const rounds = 40
	for r := 0; r < rounds; r++ {
		d := &device{buf: int64(10 + r), tag: &node{V: 7, Label: "dev"}, id: int64(1 + r%5)}
		var rwc readWriteCloser = &promoted{d}
		var m mega = &promoted{d}

		gcStress()
		want := int64(10+r) + 7
		ck(rwc.Read() == want, "embedded interface read after GC")
		ck(m.Read() == want, "mega-interface dispatch after GC")
		ck(m.Label() == "dev", "embedded labeler dispatch after GC")

		rwc.Write(int64(100 + r))
		gcStress()
		ck(rwc.Read() == int64(100+r)+7, "interface write/read after GC")

		rd, ok := any(rwc).(reader)
		ck(ok && rd.Read() == int64(100+r)+7, "interface-to-interface assertion")
		_, ok = any(rwc).(labeler)
		ck(ok, "assert labeler")
		_, ok = any(rwc).(spacer)
		ck(!ok, "assert unimplemented interface fails")

		ch := chainRW(rwc, 5)
		gcStress()
		ck(ch.Read() == int64(100+r)+7+5, "wrapper chain read after GC")
		ck(ch.Close() == d.id*1000+int64(100+r)+7, "wrapper chain close after GC")
		ck(d.closed, "close propagated through chain")

		va := vadd{inner: rwc, add: 3}
		var vr reader = va
		var vr2 reader = &va
		gcStress()
		ck(vr.Read() == int64(100+r)+10, "value receiver wrapper satisfies reader")
		ck(vr2.Read() == int64(100+r)+10, "pointer to value wrapper satisfies reader")

		ifaceSink = []any{rwc, m, ch, vr, vr2}
		gcStress()
		ck(ifaceSink[1].(mega).Label() == "dev", "global interface sink after GC")
		ck(ifaceSink[0].(readWriteCloser).Read() == int64(100+r)+7, "global interface sink dispatch")
	}
	finish("l06_interface_embedding_chain")
}
