// N06: 进程/信号生命周期长跑。
// 自 re-exec：父进程通过 STRESS_ROLE 把自身二进制分派为子模式（echo/挂起/panic/SIGQUIT）；
// 多 worker 并发 fork/exec（全局并发上限 10），pipe 读输出校验前缀，context cancel 杀挂起进程；
// 父进程校验 exit code 与输出前缀，结束统计 started/finished（僵尸差）、goroutine 基线。
// 非 Unix 平台直接跳过负载（构建期符号仍存在）。
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	roleEnv           = "STRESS_ROLE"
	childPanicMsg     = "n06-child-panic"
	maxConcurrentExec = 10
)

var (
	beats            int64
	childrenStarted  int64
	childrenFinished int64
	childrenInflight int64
	echoDone         int64
	panicDone        int64
	quitDone         int64
	cancelDone       int64

	execPath string
	jobsSem  = make(chan struct{}, maxConcurrentExec)
)

func markStarted() {
	atomic.AddInt64(&childrenStarted, 1)
	atomic.AddInt64(&childrenInflight, 1)
}

func markFinished() {
	atomic.AddInt64(&childrenFinished, 1)
	atomic.AddInt64(&childrenInflight, -1)
}

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	fmt.Fprintf(os.Stderr, "%s", buf[:n])
	os.Exit(1)
}

func envInt(key string, def, min, max int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func envSeed(def uint64) uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return def
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 {
		return def
	}
	return v
}

// childEnv 构造子进程环境：去掉 role/crash/GOTRACEBACK 继承，
// STRESS_CRASH_FILE 显式传空，避免子进程 crash 文本覆盖父进程的 crash 文件。
func childEnv(role string) []string {
	src := os.Environ()
	env := make([]string, 0, len(src)+3)
	for _, kv := range src {
		switch {
		case strings.HasPrefix(kv, roleEnv+"="):
			continue
		case strings.HasPrefix(kv, "STRESS_CRASH_FILE="):
			continue
		case strings.HasPrefix(kv, "GOTRACEBACK="):
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		roleEnv+"="+role,
		"STRESS_CRASH_FILE=",
		// 固定崩溃文本形态，保证 panic/SIGQUIT 的 exit code 与输出可判读。
		"GOTRACEBACK=default",
	)
	return env
}

func childMain(role string) {
	switch role {
	case "echo":
		fmt.Printf("CHILD-ECHO %s\n", strings.Join(os.Args[2:], " "))
		os.Exit(0)
	case "hang", "quit":
		fmt.Println("CHILD-READY")
		for {
			time.Sleep(time.Hour)
		}
	case "panic":
		fmt.Println("CHILD-BEFORE-PANIC")
		panic(childPanicMsg)
	default:
		fmt.Fprintf(os.Stderr, "unknown STRESS_ROLE %q\n", role)
		os.Exit(3)
	}
	os.Exit(0)
}

func spawnLimited(fn func()) {
	jobsSem <- struct{}{}
	go func() {
		defer func() { <-jobsSem }()
		fn()
	}()
}

// runLimited 同步执行一个子进程任务，同样受全局并发上限约束。
func runLimited(fn func()) {
	jobsSem <- struct{}{}
	defer func() { <-jobsSem }()
	fn()
}

func runEcho(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, execPath, "echo", strconv.FormatInt(id, 10))
	cmd.Env = childEnv("echo")
	markStarted()
	out, err := cmd.Output()
	markFinished()
	atomic.AddInt64(&echoDone, 1)
	if err != nil {
		failf("phase=echo id=%d: %v", id, err)
	}
	want := fmt.Sprintf("CHILD-ECHO %d", id)
	if got := strings.TrimSpace(string(out)); got != want {
		failf("phase=echo id=%d: stdout=%q want=%q", id, got, want)
	}
}

func runCancel(id int64) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, execPath, "hang")
	cmd.Env = childEnv("hang")
	pr, err := cmd.StdoutPipe()
	if err != nil {
		failf("phase=cancel id=%d: stdout pipe: %v", id, err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	markStarted()
	if err := cmd.Start(); err != nil {
		markFinished()
		failf("phase=cancel id=%d: start: %v", id, err)
	}
	r := bufio.NewReader(pr)
	line, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "CHILD-READY" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		markFinished()
		failf("phase=cancel id=%d: ready line=%q err=%v stderr=%q", id, line, err, stderr.String())
	}
	cancel()
	_, _ = io.ReadAll(r)
	werr := cmd.Wait()
	markFinished()
	atomic.AddInt64(&cancelDone, 1)
	if ctx.Err() != context.Canceled {
		failf("phase=cancel id=%d: ctx err=%v", id, ctx.Err())
	}
	if werr == nil {
		failf("phase=cancel id=%d: child exited 0 despite cancel", id)
	}
}

func runPanic(id int64) {
	cmd := exec.Command(execPath, "panic")
	cmd.Env = childEnv("panic")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	markStarted()
	err := cmd.Run()
	markFinished()
	atomic.AddInt64(&panicDone, 1)
	if err == nil {
		failf("phase=panic id=%d: child exit 0", id)
	}
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 2 {
		failf("phase=panic id=%d: exit=%v want exit code 2 (stderr=%q)", id, err, stderr.String())
	}
	if got := strings.TrimSpace(stdout.String()); got != "CHILD-BEFORE-PANIC" {
		failf("phase=panic id=%d: stdout=%q", id, got)
	}
	if !strings.HasPrefix(stderr.String(), "panic: "+childPanicMsg) {
		failf("phase=panic id=%d: stderr prefix=%q", id, headLine(stderr.String()))
	}
}

func runQuit(id int64) {
	cmd := exec.Command(execPath, "quit")
	cmd.Env = childEnv("quit")
	pr, err := cmd.StdoutPipe()
	if err != nil {
		failf("phase=sigquit id=%d: stdout pipe: %v", id, err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	markStarted()
	if err := cmd.Start(); err != nil {
		markFinished()
		failf("phase=sigquit id=%d: start: %v", id, err)
	}
	r := bufio.NewReader(pr)
	line, err := r.ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "CHILD-READY" {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		markFinished()
		failf("phase=sigquit id=%d: ready line=%q err=%v", id, line, err)
	}
	if err := cmd.Process.Signal(syscall.SIGQUIT); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		markFinished()
		failf("phase=sigquit id=%d: signal SIGQUIT: %v", id, err)
	}
	_, _ = io.ReadAll(r)
	werr := cmd.Wait()
	markFinished()
	atomic.AddInt64(&quitDone, 1)
	if werr == nil {
		failf("phase=sigquit id=%d: child exit 0 after SIGQUIT", id)
	}
	var ee *exec.ExitError
	if !errors.As(werr, &ee) || ee.ExitCode() != 2 {
		failf("phase=sigquit id=%d: exit=%v want exit code 2 (stderr=%q)", id, werr, headLine(stderr.String()))
	}
	dump := stderr.String()
	if !strings.Contains(dump, "SIGQUIT") && !strings.Contains(dump, "goroutine ") {
		failf("phase=sigquit id=%d: no runtime dump in stderr: %q", id, headLine(dump))
	}
}

func headLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

type worker struct {
	id     int64
	seed   uint64
	rounds int64
}

func (w *worker) round(r int64) {
	base := int64(w.seed)*1000 + r*100 + w.id*17
	jobs := 6 + int((w.seed+uint64(r))%4)

	var jwg sync.WaitGroup
	for i := 0; i < jobs; i++ {
		id := base + int64(i)
		jwg.Add(1)
		spawnLimited(func() {
			defer jwg.Done()
			runEcho(id)
		})
	}
	jwg.Wait()

	runLimited(func() { runCancel(base) })
	runLimited(func() { runPanic(base) })
	runLimited(func() { runQuit(base) })

	w.rounds = r
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(deadline time.Time, seconds int, fixed int64) {
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				break
			}
		} else if r >= fixed {
			break
		}
		w.round(r)
		r++
	}
}

func watchdog(interval time.Duration, stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	last := atomic.LoadInt64(&beats)
	lastChange := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			cur := atomic.LoadInt64(&beats)
			if cur != last {
				last = cur
				lastChange = time.Now()
				continue
			}
			if time.Since(lastChange) > 6*interval {
				failf("watchdog stalled: no worker progress for %s (beats=%d)", time.Since(lastChange), cur)
			}
		}
	}
}

// 把 fatal/panic 的 crash 文本写到 runner 指定的文件（core 不可用的环境用）。
func setupCrashOutput() {
	p := os.Getenv("STRESS_CRASH_FILE")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
}

func countFDs() int {
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return len(ents)
}

func main() {
	if role := os.Getenv(roleEnv); role != "" {
		childMain(role)
		return
	}
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 3, 1, 8)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 100000))

	fmt.Printf("scenario=n06 phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds exec_limit=%d\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb, maxConcurrentExec)

	if runtime.GOOS == "windows" {
		fmt.Println("skip: n06 exec/signal load requires unix (windows)")
		fmt.Println("STRESS PASS n06_exec_signal_lifecycle")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		failf("phase=preflight: os.Executable: %v", err)
	}
	execPath = exe

	startBase := runtime.NumGoroutine()
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	stop := make(chan struct{})

	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed*1000 + uint64(i) + 1}
		ws[i] = w
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(deadline, seconds, fixed)
		}()
	}
	var helpers sync.WaitGroup
	helpers.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &helpers)

	wg.Wait()
	close(stop)
	helpers.Wait()

	runtime.GC()
	final := runtime.NumGoroutine()
	started := atomic.LoadInt64(&childrenStarted)
	finished := atomic.LoadInt64(&childrenFinished)
	inflight := atomic.LoadInt64(&childrenInflight)
	if inflight != 0 || started != finished {
		failf("phase=quiesce: children started=%d finished=%d inflight=%d (zombie/wait leak)", started, finished, inflight)
	}
	if final > startBase+8 {
		failf("phase=quiesce: goroutine baseline exceeded: final=%d start=%d", final, startBase)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d beats=%d children_started=%d children_finished=%d zombies=%d echo=%d cancel=%d panic=%d sigquit=%d open_fds=%d goroutines_start=%d goroutines_final=%d heap_alloc=%d num_gc=%d\n",
		ws[0].rounds, atomic.LoadInt64(&beats), started, finished, started-finished,
		atomic.LoadInt64(&echoDone), atomic.LoadInt64(&cancelDone), atomic.LoadInt64(&panicDone), atomic.LoadInt64(&quitDone),
		countFDs(), startBase, final, m.HeapAlloc, m.NumGC)
	fmt.Println("STRESS PASS n06_exec_signal_lifecycle")
}
