// L12: 包级变量初始化期的错误路径（子进程）。Go 的包级变量初始化器没有
// 隐式 recover 边界：初始化器直接 panic 会在任何 init/main 之前以退出码 2
// 终止进程；而初始化器调用的函数内部 defer/recover 可以把 panic 转为兜底值，
// 使变量初始化继续。父进程以子进程方式验证这两种行为。
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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

func traceEqual(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func runChildMode(mode string) (string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return "", -1, err
	}
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), "L12_VARPANIC_CHILD="+mode)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), -1, ctx.Err()
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode(), nil
		}
		return string(out), -1, err
	}
	return string(out), 0, nil
}

func runChild() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	ck(beforeVar == "before", "child: package variable initializer before panic did not run")
	switch childMode {
	case "recover":
		ck(afterVar == "after", "child: variable initialization did not continue after recovered panic")
		ck(loaded != nil && loaded.Tag == "loaded" && loaded.Value == 41, "child: loaded config")
		ck(recovered != nil && recovered.Tag == "fallback" && recovered.Value == 7, "child: fallback config")
		ck(traceEqual(initTrace, "var:before", "var:after", "var:recover", "init"),
			fmt.Sprintf("child: recover trace %v", initTrace))
	case "ok":
		ck(afterVar == "after", "child: variable initialization incomplete")
		ck(loaded != nil && loaded.Tag == "loaded", "child: loaded config")
		ck(recovered != nil && recovered.Tag == "safe" && recovered.Value == 43, "child: safe config")
		ck(traceEqual(initTrace, "var:before", "var:after", "init"),
			fmt.Sprintf("child: ok trace %v", initTrace))
	default:
		ck(false, "child: unexpected mode "+childMode)
	}
	gcStress()
	ck(loaded != nil && recovered != nil && afterVar == "after", "child: state changed after GC")
	finish("l12_var_init_panic_subprocess")
}

func runParent() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	// Parent itself uses the normal path; all variable initializers completed.
	ck(childMode == "", "parent inherited child mode")
	ck(beforeVar == "before" && afterVar == "after", "parent: variable initialization order")
	ck(loaded != nil && loaded.Tag == "loaded" && loaded.Value == 41, "parent: loaded config")
	ck(recovered != nil && recovered.Tag == "safe" && recovered.Value == 43, "parent: safe config")
	ck(traceEqual(initTrace, "var:before", "var:after", "init"), fmt.Sprintf("parent trace %v", initTrace))
	parentLoaded := loaded
	parentRecovered := recovered

	// A panic in a package variable initializer must terminate with exit code 2
	// before any init function or main runs.
	out, code, runErr := runChildMode("panic")
	ck(runErr == nil, "panic child failed to execute")
	ck(code == 2, fmt.Sprintf("panic child exit code %d, want 2", code))
	ck(strings.Contains(out, "panic: l12 var init boom"), "panic child missing panic message")
	ck(strings.Contains(out, "main.loadConfig"), "panic child stack missing loadConfig")
	ck(!strings.Contains(out, "CHILD-INIT-RAN"), "init function ran after variable initializer panic")
	ck(!strings.Contains(out, "ALL PASS l12_var_init_panic_subprocess"), "main ran after variable initializer panic")

	// A recover boundary inside the called initializer function converts the
	// panic to a fallback value and initialization continues.
	out, code, runErr = runChildMode("recover")
	ck(runErr == nil, "recover child failed to execute")
	ck(code == 0, fmt.Sprintf("recover child exit code %d, want 0", code))
	ck(strings.Contains(out, "CHILD-INIT-RAN"), "recover child did not reach init")
	ck(strings.Contains(out, "ALL PASS l12_var_init_panic_subprocess"), "recover child did not reach main")
	ck(!strings.Contains(out, "panic: l12 safe load boom"), "recover child leaked an unrecovered panic")

	out, code, runErr = runChildMode("ok")
	ck(runErr == nil, "ok child failed to execute")
	ck(code == 0, fmt.Sprintf("ok child exit code %d, want 0", code))
	ck(strings.Contains(out, "CHILD-INIT-RAN"), "ok child did not reach init")
	ck(strings.Contains(out, "ALL PASS l12_var_init_panic_subprocess"), "ok child did not reach main")

	// Parent-side explicit GC and concurrent reads of the initialized state.
	var wg sync.WaitGroup
	var bad int32
	workers := 8
	start := int(seedVal % 3)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if loaded.Tag != "loaded" || recovered.Tag != "safe" || len(initTrace) != 3 {
					atomic.AddInt32(&bad, 1)
					return
				}
				_ = (start + w + i) % 3
			}
		}(w)
	}
	for r := 0; r < 2+int(seedVal%3); r++ {
		for i := 0; i < 16; i++ {
			b := make([]byte, 8192)
			b[i%len(b)] = byte(r)
			gcSink = append(gcSink[:0], b)
		}
		runtime.GC()
	}
	wg.Wait()
	gcSink = gcSink[:0]
	ck(atomic.LoadInt32(&bad) == 0, "concurrent read of variable-init state failed under GC")

	gcStress()
	ck(loaded == parentLoaded && recovered == parentRecovered, "parent config pointer changed after GC")
	ck(traceEqual(initTrace, "var:before", "var:after", "init"), "parent trace changed after GC")

	finish("l12_var_init_panic_subprocess")
}

func main() {
	if childMode != "" {
		runChild()
		return
	}
	runParent()
}
