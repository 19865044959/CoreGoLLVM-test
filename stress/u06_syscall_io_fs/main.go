// U06: syscall / 文件 I/O 长跑（P1，Unix 优先）。
//
// 四类 worker 并发推进（kind = id%4）：
//
//	pipe    os.Pipe 写读并发：writer goroutine 分块写入，reader 分块校验 checksum；
//	file    临时目录文件 roundtrip：大 payload 分块写 -> GC -> Seek 回读校验；
//	syscall syscall.Syscall(SYS_WRITE/SYS_READ) 直接读写 pipe，指针参数跨 GC 校验；
//	fd      close 后 GC 再 open 复用同一 fd（Unix），并在 fd 打开期间跨 GC 读写校验。
//
// 所有 open/close/pipe 创建与 fd 复用断言都在同一把 fdGate 下串行，保证
// “close 后新 open 拿同 fd”不被并发 open 干扰；I/O 本身并发。独立 gcChurn
// 持续 GC；收尾清理临时目录并校验为空/已删除。
//
// syscall.Syscall 在 unix 上直接进入内核，但不同平台的参数个数不同（Windows 为
// 6 参数），因此通过 reflect 取函数值在运行时调用，保证同一文件可跨平台编译；
// 非 unix / 未知 syscall 号平台在运行时跳过该子模式（仍记录 skip 指标）。
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

const (
	scenarioName = "u06_syscall_io_fs"
	phases       = 2

	kindPipe = iota
	kindFile
	kindSyscall
	kindFD
	kindCount
)

var (
	beats    int64
	started  int64
	finished int64
	gcCycles int64
	workerGC int64
	totalSum int64

	pipeRounds  int64
	fileRounds  int64
	sysRounds   int64
	fdChecks    int64
	fdSkipped   int64
	rawSkipped  int64
	pipeBytes   int64
	fileBytes   int64
	sysBytes    int64
	bytesChecks int64

	// syscall.Syscall 的平台签名不同（Windows 6 参数 / unix 4 参数）；
	// 用 reflect 持有函数值以保持单文件跨平台可编译，unix 上仍是原始系统调用。
	rawFn = reflect.ValueOf(syscall.Syscall)

	// fdGate 串行化所有 open/close/pipe 创建及 fd 复用断言。
	fdGate sync.Mutex

	gSeed   uint64
	tmpRoot string
)

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

func pat(seed uint64, wid, round int64, i int) byte {
	x := seed ^ uint64(wid)*0x9E3779B97F4A7C15 ^ uint64(round)*0xD6E8FEB86659FD93 ^ uint64(i)*0x165667B19E3779F9
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	return byte(x)
}

func fill(b []byte, seed uint64, wid, round int64, off int) {
	for i := range b {
		b[i] = pat(seed, wid, round, off+i)
	}
}

func sumBytes(b []byte) uint32 {
	var h uint32 = 2166136261
	for _, c := range b {
		h ^= uint32(c)
		h *= 16777619
	}
	return h
}

func wantSum(seed uint64, wid, round int64, off, n int) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < n; i++ {
		h ^= uint32(pat(seed, wid, round, off+i))
		h *= 16777619
	}
	return h
}

// rwTraps 返回当前平台的 read/write 系统调用号；不认识的平台返回 ok=false。
func rwTraps() (read, write uintptr, ok bool) {
	switch runtime.GOOS {
	case "linux":
		switch runtime.GOARCH {
		case "amd64":
			return 0, 1, true
		case "386", "arm", "ppc64", "ppc64le", "s390x":
			return 3, 4, true
		case "arm64", "riscv64", "loong64":
			return 63, 64, true
		case "mips", "mipsle":
			return 4003, 4004, true
		case "mips64", "mips64le":
			return 5003, 5004, true
		}
	case "darwin", "freebsd", "netbsd", "openbsd", "dragonfly":
		return 3, 4, true
	}
	return 0, 0, false
}

// unixLike 决定是否做 fd 复用断言（Windows/Plan9 等 fd 语义不保证）。
func unixLike() bool {
	switch runtime.GOOS {
	case "windows", "plan9", "js", "wasip1":
		return false
	}
	return true
}

// rawCall 调用 syscall.Syscall(trap, a1, a2, a3)（unix 4 参数签名）。
func rawCall(trap, a1, a2, a3 uintptr) (uintptr, syscall.Errno) {
	out := rawFn.Call([]reflect.Value{
		reflect.ValueOf(trap), reflect.ValueOf(a1), reflect.ValueOf(a2), reflect.ValueOf(a3),
	})
	return uintptr(out[0].Uint()), syscall.Errno(out[2].Uint())
}

func rawWriteFull(trap, fd uintptr, b []byte) error {
	off := 0
	for off < len(b) {
		n, errno := rawCall(trap, fd, uintptr(unsafe.Pointer(&b[off])), uintptr(len(b)-off))
		runtime.KeepAlive(b)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		off += int(n)
	}
	return nil
}

func rawReadFull(trap, fd uintptr, b []byte) error {
	off := 0
	for off < len(b) {
		n, errno := rawCall(trap, fd, uintptr(unsafe.Pointer(&b[off])), uintptr(len(b)-off))
		runtime.KeepAlive(b)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		if n == 0 {
			return io.EOF
		}
		off += int(n)
	}
	return nil
}

func writeFull(f *os.File, b []byte) error {
	for len(b) > 0 {
		n, err := f.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

func openPipeGated(phase string, w *worker, r int64) (*os.File, *os.File) {
	fdGate.Lock()
	rp, wp, err := os.Pipe()
	fdGate.Unlock()
	if err != nil {
		w.failCtx(phase, r, "pipe-open", "os.Pipe: %v", err)
	}
	return rp, wp
}

func openFileGated(phase string, w *worker, r int64, path string, flag int) *os.File {
	fdGate.Lock()
	f, err := os.OpenFile(path, flag, 0o600)
	fdGate.Unlock()
	if err != nil {
		w.failCtx(phase, r, "file-open", "%s: %v", path, err)
	}
	return f
}

func closeGated(fs ...*os.File) {
	fdGate.Lock()
	for _, f := range fs {
		if f != nil {
			_ = f.Close()
		}
	}
	fdGate.Unlock()
}

type worker struct {
	kind int
	id   int64
	seed uint64
	next int64

	localSum int64
}

func newWorker(kind int, id int64, seed uint64) *worker {
	return &worker{kind: kind, id: id, seed: seed}
}

func (w *worker) failCtx(phase string, r int64, where, format string, args ...interface{}) {
	prefix := fmt.Sprintf("phase=%s worker=%d kind=%d round=%d seed=%d gomaxprocs=%d where=%s: ",
		phase, w.id, w.kind, r, w.seed, runtime.GOMAXPROCS(0), where)
	failf(prefix+format, args...)
}

func (w *worker) checkChunk(phase string, r int64, where string, b []byte, off int) {
	if got, want := sumBytes(b), wantSum(w.seed, w.id, r, off, len(b)); got != want {
		w.failCtx(phase, r, where, "checksum=%#x want=%#x (内容损坏)", got, want)
	}
	if len(b) > 0 {
		if got, want := b[0], pat(w.seed, w.id, r, off); got != want {
			w.failCtx(phase, r, where, "first byte=%#x want=%#x", got, want)
		}
		if got, want := b[len(b)-1], pat(w.seed, w.id, r, off+len(b)-1); got != want {
			w.failCtx(phase, r, where, "last byte=%#x want=%#x", got, want)
		}
	}
	atomic.AddInt64(&bytesChecks, 1)
}

func (w *worker) pipeRound(phaseName string, r int64, phase int) {
	chunk := 4096
	nch := 32
	if phase > 0 {
		chunk = 8192
		nch = 48
	}
	rp, wp := openPipeGated(phaseName, w, r)

	errc := make(chan error, 1)
	go func() {
		buf := make([]byte, chunk)
		for c := 0; c < nch; c++ {
			fill(buf, w.seed, w.id, r, c*chunk)
			if err := writeFull(wp, buf); err != nil {
				errc <- fmt.Errorf("write chunk=%d: %w", c, err)
				return
			}
		}
		errc <- nil
	}()

	rbuf := make([]byte, chunk)
	var sum int64
	for c := 0; c < nch; c++ {
		if _, err := io.ReadFull(rp, rbuf); err != nil {
			closeGated(rp, wp)
			w.failCtx(phaseName, r, "pipe-read", "chunk=%d: %v", c, err)
		}
		w.checkChunk(phaseName, r, "pipe-chunk", rbuf, c*chunk)
		sum += int64(chunk)
	}
	if err := <-errc; err != nil {
		closeGated(rp, wp)
		w.failCtx(phaseName, r, "pipe-write", "%v", err)
	}
	closeGated(rp, wp)
	atomic.AddInt64(&pipeBytes, sum)
	atomic.AddInt64(&pipeRounds, 1)
	atomic.AddInt64(&totalSum, sum)
}

func (w *worker) fileRound(phaseName string, r int64, phase int) {
	chunk := 4096
	nch := 32 // 128KB payload
	if phase > 0 {
		chunk = 8192
		nch = 24 // 192KB payload
	}
	path := filepath.Join(tmpRoot, fmt.Sprintf("file-%d-%d.bin", w.id, r))
	f := openFileGated(phaseName, w, r, path, os.O_RDWR|os.O_CREATE|os.O_TRUNC)

	buf := make([]byte, chunk)
	for c := 0; c < nch; c++ {
		fill(buf, w.seed, w.id, r, c*chunk)
		if err := writeFull(f, buf); err != nil {
			closeGated(f)
			w.failCtx(phaseName, r, "file-write", "chunk=%d: %v", c, err)
		}
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		closeGated(f)
		w.failCtx(phaseName, r, "file-seek", "%v", err)
	}

	runtime.GC() // fd 打开期间跨 GC，之后继续用同一 fd 读
	atomic.AddInt64(&workerGC, 1)

	for c := 0; c < nch; c++ {
		if _, err := io.ReadFull(f, buf); err != nil {
			closeGated(f)
			w.failCtx(phaseName, r, "file-read", "chunk=%d: %v", c, err)
		}
		w.checkChunk(phaseName, r, "file-chunk", buf, c*chunk)
	}
	closeGated(f)
	if err := os.Remove(path); err != nil {
		w.failCtx(phaseName, r, "file-remove", "%s: %v", path, err)
	}
	atomic.AddInt64(&fileBytes, int64(chunk*nch))
	atomic.AddInt64(&fileRounds, 1)
}

func (w *worker) syscallRound(phaseName string, r int64, phase int) {
	readTrap, writeTrap, ok := rwTraps()
	if !ok {
		atomic.AddInt64(&rawSkipped, 1)
		return
	}
	chunk := 4096
	nch := 8
	if phase > 0 {
		chunk = 8192
		nch = 16
	}
	rp, wp := openPipeGated(phaseName, w, r)
	fdR := rp.Fd()
	fdW := wp.Fd()
	runtime.KeepAlive(rp)
	runtime.KeepAlive(wp)

	buf := make([]byte, chunk)
	rbuf := make([]byte, chunk)
	for c := 0; c < nch; c++ {
		fill(buf, w.seed, w.id, r, c*chunk)
		if err := rawWriteFull(writeTrap, fdW, buf); err != nil {
			closeGated(rp, wp)
			w.failCtx(phaseName, r, "syscall-write", "trap=%d chunk=%d: %v", writeTrap, c, err)
		}
		if c%4 == 3 {
			runtime.GC() // 缓冲指针的生命周期跨过 GC，再读回校验
			atomic.AddInt64(&workerGC, 1)
		}
		if err := rawReadFull(readTrap, fdR, rbuf); err != nil {
			closeGated(rp, wp)
			w.failCtx(phaseName, r, "syscall-read", "trap=%d chunk=%d: %v", readTrap, c, err)
		}
		w.checkChunk(phaseName, r, "syscall-chunk", rbuf, c*chunk)
		runtime.KeepAlive(buf)
		runtime.KeepAlive(rbuf)
	}
	closeGated(rp, wp)
	atomic.AddInt64(&sysBytes, int64(chunk*nch))
	atomic.AddInt64(&sysRounds, 1)
}

func (w *worker) fdRound(phaseName string, r int64, phase int) {
	chunk := 4096
	nch := 4
	if phase > 0 {
		chunk = 8192
		nch = 8
	}
	path := filepath.Join(tmpRoot, fmt.Sprintf("fd-%d-%d.bin", w.id, r))

	if unixLike() {
		// close -> GC -> open 必须拿回同一 fd；整个过程持有 fdGate，排除并发 open 干扰。
		fdGate.Lock()
		f1, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			fdGate.Unlock()
			w.failCtx(phaseName, r, "fd-open-first", "%s: %v", path, err)
		}
		fd1 := int(f1.Fd())
		if err := f1.Close(); err != nil {
			fdGate.Unlock()
			w.failCtx(phaseName, r, "fd-close-first", "%v", err)
		}
		runtime.GC()
		atomic.AddInt64(&workerGC, 1)
		f2, err := os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			fdGate.Unlock()
			w.failCtx(phaseName, r, "fd-open-second", "%s: %v", path, err)
		}
		fd2 := int(f2.Fd())
		fdGate.Unlock()
		if fd2 != fd1 {
			w.failCtx(phaseName, r, "fd-reuse", "fd not reused across GC: first=%d second=%d (fd 生命周期错误)", fd1, fd2)
		}
		atomic.AddInt64(&fdChecks, 1)

		buf := make([]byte, chunk)
		for c := 0; c < nch; c++ {
			fill(buf, w.seed, w.id, r, c*chunk)
			if err := writeFull(f2, buf); err != nil {
				closeGated(f2)
				w.failCtx(phaseName, r, "fd-write", "%v", err)
			}
		}
		runtime.GC() // 复用出来的 fd 在 GC 后继续有效
		atomic.AddInt64(&workerGC, 1)
		if _, err := f2.Seek(0, io.SeekStart); err != nil {
			closeGated(f2)
			w.failCtx(phaseName, r, "fd-seek", "%v", err)
		}
		for c := 0; c < nch; c++ {
			if _, err := io.ReadFull(f2, buf); err != nil {
				closeGated(f2)
				w.failCtx(phaseName, r, "fd-read", "chunk=%d: %v", c, err)
			}
			w.checkChunk(phaseName, r, "fd-chunk", buf, c*chunk)
		}
		closeGated(f2)
	} else {
		atomic.AddInt64(&fdSkipped, 1)
		f := openFileGated(phaseName, w, r, path, os.O_RDWR|os.O_CREATE|os.O_TRUNC)
		buf := make([]byte, chunk)
		fill(buf, w.seed, w.id, r, 0)
		if err := writeFull(f, buf); err != nil {
			closeGated(f)
			w.failCtx(phaseName, r, "fd-write", "%v", err)
		}
		runtime.GC()
		atomic.AddInt64(&workerGC, 1)
		closeGated(f)
	}
	if err := os.Remove(path); err != nil {
		w.failCtx(phaseName, r, "fd-remove", "%s: %v", path, err)
	}
	atomic.AddInt64(&totalSum, int64(w.id*1000+r))
}

func (w *worker) round(phaseName string, r int64, phase int) {
	switch w.kind {
	case kindPipe:
		w.pipeRound(phaseName, r, phase)
	case kindFile:
		w.fileRound(phaseName, r, phase)
	case kindSyscall:
		w.syscallRound(phaseName, r, phase)
	default:
		w.fdRound(phaseName, r, phase)
	}
	atomic.AddInt64(&beats, 1)
}

func (w *worker) run(phaseName string, phase int, deadline time.Time, timed bool, stopRound int64) {
	atomic.AddInt64(&started, 1)
	defer atomic.AddInt64(&finished, 1)
	for {
		if timed {
			if !time.Now().Before(deadline) {
				return
			}
		} else if w.next >= stopRound {
			return
		}
		r := w.next
		w.round(phaseName, r, phase)
		w.next = r + 1
	}
}

// gcChurn 持续分配 + GC，与 pipe/file/syscall/fd 路径并发。
func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 12; i++ {
			b := make([]byte, 4096)
			b[i%len(b)] = byte(i)
			sink = append(sink, b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		time.Sleep(time.Millisecond)
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
				failf("phase=watchdog seed=%d: no worker progress for %s (beats=%d)", gSeed, time.Since(lastChange), cur)
			}
		}
	}
}

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

func waitGoroutines(base int, grace time.Duration) int {
	giveUp := time.Now().Add(grace)
	for {
		n := runtime.NumGoroutine()
		if n <= base || time.Now().After(giveUp) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func checkTmpEmpty(where string) {
	ents, err := os.ReadDir(tmpRoot)
	if err != nil {
		failf("phase=%s seed=%d: read temp dir %s: %v", where, gSeed, tmpRoot, err)
	}
	if len(ents) != 0 {
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		failf("phase=%s seed=%d: temp dir %s not empty: %v (文件未清理)", where, gSeed, tmpRoot, names)
	}
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 8, 1, 16)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 8, 1, 1000000))
	gSeed = seed

	readTrap, writeTrap, rawOK := rwTraps()
	dir, err := os.MkdirTemp("", "corego-u06-*")
	if err != nil {
		failf("phase=preflight seed=%d: MkdirTemp: %v", seed, err)
	}
	tmpRoot = dir

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d heartbeat=%ds phases=%d fixed_rounds=%d unix_like=%v raw_syscall=%v read_trap=%d write_trap=%d tmp=%s\n",
		scenarioName, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, hb, phases, fixed,
		unixLike(), rawOK, readTrap, writeTrap, tmpRoot)

	runtime.GC()
	startBase := runtime.NumGoroutine()

	stop := make(chan struct{})
	var aux sync.WaitGroup
	aux.Add(1)
	go gcChurn(stop, &aux)
	aux.Add(1)
	go watchdog(time.Duration(hb)*time.Second, stop, &aux)

	ws := make([]*worker, workers)
	kindCounts := [kindCount]int{}
	for i := 0; i < workers; i++ {
		kind := i % kindCount
		kindCounts[kind]++
		ws[i] = newWorker(kind, int64(i), seed*1_000_003+uint64(i+1)*7919)
	}

	start := time.Now()
	var total time.Duration
	if seconds > 0 {
		total = time.Duration(seconds) * time.Second
	}

	for p := 0; p < phases; p++ {
		pStart := fixed * int64(p) / int64(phases)
		pEnd := fixed * int64(p+1) / int64(phases)
		var deadline time.Time
		phaseName := fmt.Sprintf("p%d", p)
		if seconds > 0 {
			if p == phases-1 {
				deadline = start.Add(total)
			} else {
				deadline = start.Add(total * time.Duration(p+1) / time.Duration(phases))
			}
			fmt.Printf("phase=%s chunk_scale=%d timed=true\n", phaseName, 1+p)
		} else {
			fmt.Printf("phase=%s chunk_scale=%d rounds=[%d,%d)\n", phaseName, 1+p, pStart, pEnd)
		}

		var wg sync.WaitGroup
		for _, w := range ws {
			wg.Add(1)
			go func(w *worker, phase int, name string) {
				defer wg.Done()
				w.run(name, phase, deadline, seconds > 0, pEnd)
			}(w, p, phaseName)
		}
		wg.Wait()
		checkTmpEmpty(phaseName + "-boundary")
	}

	close(stop)
	aux.Wait()

	runtime.GC()
	runtime.GC()

	if s, f := atomic.LoadInt64(&started), atomic.LoadInt64(&finished); s != f || s != int64(workers*phases) {
		failf("phase=final seed=%d: started=%d finished=%d want=%d", seed, s, f, workers*phases)
	}

	var roundsTotal int64
	for _, w := range ws {
		roundsTotal += w.next
		if seconds == 0 && w.next != fixed {
			failf("phase=final worker=%d seed=%d: rounds=%d want=%d", w.id, w.seed, w.next, fixed)
		}
	}
	if roundsTotal == 0 {
		failf("phase=final seed=%d: no rounds completed", seed)
	}
	if kindCounts[kindPipe] > 0 && atomic.LoadInt64(&pipeRounds) == 0 {
		failf("phase=final seed=%d: pipe subtest never ran", seed)
	}
	if kindCounts[kindFile] > 0 && atomic.LoadInt64(&fileRounds) == 0 {
		failf("phase=final seed=%d: file subtest never ran", seed)
	}
	if kindCounts[kindSyscall] > 0 {
		if rawOK && atomic.LoadInt64(&sysRounds) == 0 {
			failf("phase=final seed=%d: syscall subtest never ran (raw supported)", seed)
		}
		if !rawOK && atomic.LoadInt64(&rawSkipped) == 0 {
			failf("phase=final seed=%d: syscall subtest neither ran nor skipped", seed)
		}
	}
	if kindCounts[kindFD] > 0 {
		if unixLike() && atomic.LoadInt64(&fdChecks) == 0 {
			failf("phase=final seed=%d: fd reuse subtest never ran (unix)", seed)
		}
		if !unixLike() && atomic.LoadInt64(&fdSkipped) == 0 {
			failf("phase=final seed=%d: fd reuse subtest neither ran nor skipped", seed)
		}
	}
	if atomic.LoadInt64(&bytesChecks) == 0 {
		failf("phase=final seed=%d: no checksum checks", seed)
	}

	checkTmpEmpty("final-before-remove")
	if err := os.RemoveAll(tmpRoot); err != nil {
		failf("phase=final seed=%d: RemoveAll(%s): %v", seed, tmpRoot, err)
	}
	if _, err := os.Stat(tmpRoot); !os.IsNotExist(err) {
		failf("phase=final seed=%d: temp dir %s still exists after cleanup: %v", seed, tmpRoot, err)
	}

	ng := waitGoroutines(startBase, 5*time.Second)
	if ng > startBase+8 {
		failf("phase=final seed=%d: goroutines baseline=%d after=%d: goroutine leak", seed, startBase, ng)
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: rounds=%d pipe_rounds=%d file_rounds=%d syscall_rounds=%d fd_checks=%d fd_skipped=%d syscall_skipped=%d pipe_bytes=%d file_bytes=%d syscall_bytes=%d chunk_checks=%d gc_churn=%d worker_gc=%d num_gc=%d total_sum=%d goroutines_start=%d goroutines_final=%d heap_alloc=%d heap_objects=%d gomaxprocs=%d\n",
		roundsTotal, atomic.LoadInt64(&pipeRounds), atomic.LoadInt64(&fileRounds), atomic.LoadInt64(&sysRounds),
		atomic.LoadInt64(&fdChecks), atomic.LoadInt64(&fdSkipped), atomic.LoadInt64(&rawSkipped),
		atomic.LoadInt64(&pipeBytes), atomic.LoadInt64(&fileBytes), atomic.LoadInt64(&sysBytes),
		atomic.LoadInt64(&bytesChecks), atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&workerGC), m.NumGC,
		atomic.LoadInt64(&totalSum), startBase, ng, m.HeapAlloc, m.HeapObjects, runtime.GOMAXPROCS(0))
	fmt.Printf("STRESS PASS %s\n", scenarioName)
}
