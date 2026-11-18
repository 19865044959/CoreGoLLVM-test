// L15: 构建标签选择的接口实现。默认 !tagAlt 选择 baseSpeaker，-tags=tagAlt
// 选择 altSpeaker；两者都满足 Speaker，运行期通过接口分派、类型 switch 与
// 反射类型名确认实际编译进来的实现。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"sync/atomic"
)

var (
	failed int32
	gcSink [][]byte
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

func main() {
	want := os.Getenv("EXPECT_IMPL")
	if want == "" {
		want = "base"
	}
	wantSay := "base:hello"
	wantType := "baseSpeaker"
	if want == "alt" {
		wantSay = "alt:hello"
		wantType = "altSpeaker"
	}

	msg := &Message{ID: 31, Text: "hello"}
	msg.Next = &Message{ID: 32, Text: "next"}
	var s Speaker = newSpeaker(msg)
	gcStress()

	ck(s.Name() == want, "build tag selects interface implementation")
	ck(s.Say() == wantSay, "interface dispatch reaches tag-selected method")

	rt := reflect.TypeOf(s)
	ck(rt.Kind() == reflect.Ptr && rt.Elem().Name() == wantType, "reflection sees selected concrete type")

	ck(msg.ID == 31 && msg.Text == "hello" && msg.Next != nil && msg.Next.ID == 32, "interface payload after GC")
	finish("l15_tag_iface_impl")
}
