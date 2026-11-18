// L12: init panic/exit boundaries in a blank-imported package, observed by
// re-executing the test binary as a subprocess. Modes cover panic, panic(nil)
// (Go 1.24 *runtime.PanicNilError), panic from a deferred init function,
// os.Exit inside a deferred init function, os.Exit(7) directly in init, init
// recover and the normal path. The parent asserts exit status and markers,
// proving main never ran on the fatal paths.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "l12_init_panic_nested_subprocess/boom"
)

type probeData struct {
	tag     string
	payload []byte
}

var probe = &probeData{tag: "parent-probe", payload: []byte("parent-payload")}

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

type childCase struct {
	mode   string
	code   int
	want   []string
	forbid []string
}

func main() {
	if os.Getenv("L12_INIT_CHILD") != "" {
		// The init functions that reach here survived. Only the main-visible
		// marker is printed; this binary never prints ALL PASS.
		fmt.Println("CHILD-OK")
		return
	}

	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	exe, err := os.Executable()
	ck(err == nil && exe != "", "os.Executable")

	cases := []childCase{
		{mode: "panic", code: 2, want: []string{"panic: nested init boom"}, forbid: []string{"CHILD-OK", "ALL PASS"}},
		{mode: "nil", code: 2, want: []string{"panic: panic called with nil argument"}, forbid: []string{"CHILD-OK", "ALL PASS"}},
		{mode: "deferpanic", code: 2, want: []string{"panic: defer boom"}, forbid: []string{"CHILD-OK", "ALL PASS"}},
		{mode: "deferexit", code: 0, forbid: []string{"CHILD-OK", "ALL PASS"}},
		{mode: "exit7", code: 7, forbid: []string{"CHILD-OK", "ALL PASS"}},
		{mode: "recover", code: 0, want: []string{"CHILD-OK"}, forbid: []string{"panic:", "FAIL", "ALL PASS"}},
		{mode: "ok", code: 0, want: []string{"CHILD-OK"}, forbid: []string{"panic:", "FAIL", "ALL PASS"}},
	}

	for _, tc := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, exe)
		cmd.Env = append(os.Environ(), "L12_INIT_CHILD="+tc.mode)
		out, runErr := cmd.CombinedOutput()
		cancel()

		code := 0
		switch e := runErr.(type) {
		case nil:
		case *exec.ExitError:
			code = e.ExitCode()
			if code < 0 {
				ck(false, "subprocess "+tc.mode+" killed/timeout")
			}
		default:
			ck(false, "subprocess "+tc.mode+" start error: "+fmt.Sprint(runErr))
		}

		text := string(out)
		ck(code == tc.code, fmt.Sprintf("mode %s exit %d want %d", tc.mode, code, tc.code))
		for _, w := range tc.want {
			ck(strings.Contains(text, w), fmt.Sprintf("mode %s output missing %q", tc.mode, w))
		}
		for _, f := range tc.forbid {
			ck(!strings.Contains(text, f), fmt.Sprintf("mode %s output unexpectedly contains %q", tc.mode, f))
		}
		ck(!strings.Contains(text, "ALL PASS l12_init_panic_nested_subprocess"), "child printed parent pass marker")
	}

	// Explicit GC on a pointer graph while the subprocess results are checked.
	gcStress(2 + int(seedVal%3))
	ck(probe != nil && probe.tag == "parent-probe" && string(probe.payload) == "parent-payload", "parent probe changed after GC")
	ck(len(cases) == 7, "case table changed after GC")

	finish("l12_init_panic_nested_subprocess")
}
