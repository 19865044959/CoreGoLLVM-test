// L12: init 错误处理。init 失败无法返回 error，生产代码常见两种收敛方式：
// panic（运行时以退出码 2 终止、main 不执行）与 os.Exit（自定义退出码）。
// 本用例通过“自身作为子进程”的方式观察这两种失败路径，并验证 init 内
// defer/recover 能把 panic 收敛后继续运行 main。子进程输出不打印到父进程。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const caseName = "l12_init_panic_subprocess"

const childEnv = "L12_INIT_CHILD_MODE"

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	childMode   = os.Getenv(childEnv)
	recovered   bool
	recoverNote string
	initRan     int32
)

func init() {
	atomic.AddInt32(&initRan, 1)
	switch childMode {
	case "panic":
		panic("l12 init boom")
	case "exit":
		os.Exit(7)
	case "recover":
		func() {
			defer func() {
				if r := recover(); r != nil {
					recovered = true
					recoverNote = fmt.Sprint(r)
				}
			}()
			panic("l12 init boom")
		}()
	}
}

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

func runChildProcess(exe, mode string) (int, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), childEnv+"="+mode)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return 0, string(out)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), string(out)
	}
	return -1, string(out)
}

func runChildAsserts() {
	switch childMode {
	case "recover":
		ck(recovered, "init recovered its own panic")
		ck(recoverNote == "l12 init boom", "recovered value text")
	case "ok":
		ck(!recovered, "no recovery in normal mode")
	}
	ck(atomic.LoadInt32(&initRan) == 1, "init ran exactly once")
	gcStress()
	finish(caseName)
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	if childMode != "" {
		runChildAsserts()
		return
	}

	exe, err := os.Executable()
	ck(err == nil, "os.Executable")
	if err != nil {
		finish(caseName)
		return
	}

	// init panic: runtime reports the panic and exits with status 2; main never
	// runs, so no pass marker may appear.
	code, out := runChildProcess(exe, "panic")
	ck(code == 2, "panic in init exits with status 2")
	ck(strings.Contains(out, "panic: l12 init boom"), "panic value reported")
	ck(!strings.Contains(out, "ALL PASS "+caseName), "main did not run after init panic")
	ck(!strings.Contains(out, "FAILED "+caseName), "panic failure not masked")

	// init os.Exit: custom status, no panic text, main never runs.
	code, out = runChildProcess(exe, "exit")
	ck(code == 7, "os.Exit in init preserves exit status")
	ck(!strings.Contains(out, "panic: l12 init boom"), "exit path does not panic")
	ck(!strings.Contains(out, "ALL PASS "+caseName), "main did not run after init exit")

	// init defer/recover: process continues to main with the recorded error.
	code, out = runChildProcess(exe, "recover")
	ck(code == 0, "recovered init panic lets main run")
	ck(strings.Contains(out, "ALL PASS "+caseName), "recovered child passes")
	ck(!strings.Contains(out, "FAIL:"), "recovered child has no failed checks")

	// Normal child: sanity check that the harness itself works.
	code, out = runChildProcess(exe, "ok")
	ck(code == 0, "normal child exits 0")
	ck(strings.Contains(out, "ALL PASS "+caseName), "normal child passes")

	// Explicit GC, with a seed-dependent number of rounds.
	rounds := 2 + int(seedVal%3)
	for i := 0; i < rounds; i++ {
		gcStress()
	}
	ck(atomic.LoadInt32(&initRan) == 1, "parent init ran exactly once")

	finish(caseName)
}
