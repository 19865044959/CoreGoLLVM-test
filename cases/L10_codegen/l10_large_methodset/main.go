// L10: 生成风格大方法集（160 个接口方法）通过接口分派调用，GC 前后结果一致。
// P2：TORTURE_ROUNDS 放大轮次。
package main

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
)

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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	return v
}

func expectedAll(impl *bigImpl, x int64) int64 {
	sum := int64(0)
	for i := 0; i < bigMethodCount; i++ {
		sum += x + impl.bias + impl.data[i] + impl.pl.V + int64(i)
	}
	return sum
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	torture := envInt("TORTURE_ROUNDS", 1)
	baseRounds := 64
	if torture > 200 {
		torture = 200
	}
	rounds := baseRounds * torture

	// 1. 接口方法数与方法集完整性。
	ityp := reflect.TypeOf((*bigAPI)(nil)).Elem()
	ck(ityp.Kind() == reflect.Interface, "bigAPI is interface")
	ck(ityp.NumMethod() == bigMethodCount, fmt.Sprintf("interface method count %d", ityp.NumMethod()))
	ck(bigMethodCount == 160, "generated method count")

	data := make([]int64, bigMethodCount)
	for i := range data {
		data[i] = int64((i*37)%101) - 50
	}
	impl := &bigImpl{bias: 7, data: data, pl: &bigPayload{V: 11, S: "payload"}, name: "impl"}
	var api bigAPI = impl
	ck(api != nil, "interface assignment")
	if _, ok := api.(*bigImpl); !ok {
		ck(false, "type assertion back to impl")
	}

	// 2. 直接接口调用与全方法集汇总。
	x := int64(13)
	ck(callAll(api, x) == expectedAll(impl, x), "callAll initial")

	// 3. 方法值捕获 receiver，GC 后仍可调用。
	mv := api.Op042
	mv2 := api.Op159
	gcSink = append(gcSink, []byte(impl.name))
	gcStress()
	want42 := x + impl.bias + data[42] + impl.pl.V + 42
	want159 := x + impl.bias + data[159] + impl.pl.V + 159
	ck(mv(x) == want42, "method value after GC")
	ck(mv2(x) == want159, "last method value after GC")
	ck(callAll(api, x) == expectedAll(impl, x), "callAll after GC")

	// 4. 多轮：GC 穿插、数据变更后接口视图同步、receiver 指针存活。
	for round := 0; round < rounds; round++ {
		if round%2 == 0 {
			gcStress()
		}
		impl.data[round%bigMethodCount] = int64(round*3 - 17)
		impl.pl.V = int64(round % 23)
		impl.bias = int64(round%5) - 2
		impl.name = fmt.Sprintf("impl-%d", round)
		got := callAll(api, int64(round))
		want := expectedAll(impl, int64(round))
		if got != want {
			ckSeed(false, fmt.Sprintf("round %d callAll got %d want %d", round, got, want))
			break
		}
		back, ok := api.(*bigImpl)
		if !ok || back != impl || back.name != impl.name {
			ckSeed(false, fmt.Sprintf("round %d receiver identity", round))
			break
		}
		// 抽查若干方法（含首尾）。
		for _, idx := range []int{0, 17, 63, 64, 128, bigMethodCount - 1} {
			var v int64
			switch idx {
			case 0:
				v = api.Op000(int64(round))
			case 17:
				v = api.Op017(int64(round))
			case 63:
				v = api.Op063(int64(round))
			case 64:
				v = api.Op064(int64(round))
			case 128:
				v = api.Op128(int64(round))
			default:
				v = api.Op159(int64(round))
			}
			want := int64(round) + impl.bias + impl.data[idx] + impl.pl.V + int64(idx)
			if v != want {
				ckSeed(false, fmt.Sprintf("round %d method %d", round, idx))
				break
			}
		}
	}

	// 5. torture 放大后仍保持确定性（相同输入同结果）。
	if torture > 1 {
		impl.bias = 0
		impl.pl.V = 0
		for i := range impl.data {
			impl.data[i] = int64(i)
		}
		gcStress()
		first := callAll(api, 1)
		second := callAll(api, 1)
		ck(first == second, "torture deterministic")
		// 手动核对：160*(x+bias+pv) + sum(i) + sum(i)
		want := int64(bigMethodCount)*1 + int64(bigMethodCount*(bigMethodCount-1)) // 2*sum(i)
		ck(first == want, "torture formula")
	}

	finish("l10_large_methodset")
}
