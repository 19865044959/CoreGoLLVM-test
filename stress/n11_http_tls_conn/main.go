// N11: HTTP/TLS 连接状态机长跑。
// httptest TLS server + 并发客户端 worker：
//   - Transport 连接池复用（MaxIdleConns/MaxIdleConnsPerHost 限制，IdleConnTimeout）；
//   - 随机 cancel-in-dial（自定义 DialContext 派生可取消 ctx）；
//   - server 按确定性 hash 对少量请求 hijack（半截响应/垃圾字节）或直接早关；
//   - 小比例请求使用很短的 context deadline，失败后进行有界重试。
//
// 成功响应校验 id + crc32；失败只做分类统计（不依赖墙钟断言）；结束 CloseIdleConnections
// + srv.Close 后检查 goroutine 回到基线。
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const scenario = "n11"

var (
	beats    int64
	gcCycles int64

	attempts    int64
	oks         int64
	logicalFail int64
	reusedConns int64
	newConns    int64
	dialChaos   int64
	retries     int64

	errCanceled  int64
	errDeadline  int64
	errTimeout   int64
	errEOF       int64
	errReset     int64
	errMalformed int64

	served   int64
	hijacked int64
	earlyout int64

	dialSeq int64

	shortDeadlineConns int64
)

type worker struct {
	id     int64
	seed   uint64
	client *http.Client
	url    string
	rng    uint64
}

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	buf := make([]byte, 4<<20)
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

func (w *worker) next() uint64 {
	x := w.rng
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	w.rng = x
	return x
}

// chaosFor 由客户端请求 id + seed 确定性决定服务端行为：~3% 的请求进入异常路径。
func chaosFor(id int64, seed uint64) int {
	h := uint64(id)*0x9e3779b97f4a7c15 + seed*0xbf58476d1ce4e5b9
	h ^= h >> 29
	h *= 0x94d049bb133111eb
	h ^= h >> 32
	return int(h % 97)
}

func validBody(b []byte) bool {
	if len(b) == 0 || len(b) > 4096 {
		return false
	}
	s := string(b)
	return strings.HasPrefix(s, "w=") && strings.Contains(s, ":r=") && strings.Contains(s, ":a=")
}

// serverHandler 在少量连接上 hijack 后写半截响应/垃圾字节或直接关闭；其余正常校验并回 checksum。
func serverHandler(seed uint64) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		switch chaosFor(id, seed) {
		case 0:
			if hj, ok := w.(http.Hijacker); ok {
				c, _, err := hj.Hijack()
				if err == nil {
					atomic.AddInt64(&earlyout, 1)
					_ = c.Close()
					return
				}
			}
		case 1:
			if hj, ok := w.(http.Hijacker); ok {
				c, _, err := hj.Hijack()
				if err == nil {
					atomic.AddInt64(&hijacked, 1)
					_, _ = c.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 64\r\n\r\npartial"))
					_ = c.Close()
					return
				}
			}
		case 2:
			if hj, ok := w.(http.Hijacker); ok {
				c, _, err := hj.Hijack()
				if err == nil {
					atomic.AddInt64(&hijacked, 1)
					_, _ = c.Write([]byte("NOT-HTTP partial garbage\r\n\r\n"))
					_ = c.Close()
					return
				}
			}
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil || !validBody(body) {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		atomic.AddInt64(&served, 1)
		fmt.Fprintf(w, "%d %d", id, crc32.ChecksumIEEE(body))
	}
}

// classifyErr 把请求错误归入预期类别；ok=false 表示未知错误（应视为失败）。
func classifyErr(err error) (string, bool) {
	if err == nil {
		return "", true
	}
	if errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled") ||
		strings.Contains(err.Error(), "request canceled") {
		return "canceled", true
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "deadline exceeded") {
		return "deadline", true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout", true
	}
	if errors.Is(err, io.EOF) {
		return "eof", true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected-eof", true
	}
	s := err.Error()
	for _, sub := range []string{
		"connection reset", "connection aborted", "broken pipe", "forcibly closed",
		"use of closed network connection", "server closed idle connection",
	} {
		if strings.Contains(s, sub) {
			return "reset", true
		}
	}
	for _, sub := range []string{
		"transport connection broken", "malformed HTTP", "unexpected EOF", "server closed",
	} {
		if strings.Contains(s, sub) {
			return "malformed", true
		}
	}
	return "unknown", false
}

func recordErr(cat string) {
	switch cat {
	case "canceled":
		atomic.AddInt64(&errCanceled, 1)
	case "deadline":
		atomic.AddInt64(&errDeadline, 1)
	case "timeout":
		atomic.AddInt64(&errTimeout, 1)
	case "eof", "unexpected-eof":
		atomic.AddInt64(&errEOF, 1)
	case "reset":
		atomic.AddInt64(&errReset, 1)
	case "malformed":
		atomic.AddInt64(&errMalformed, 1)
	}
}

// attempt 发送一次请求并校验成功响应；返回 nil 表示成功，否则返回分类后的错误。
func (w *worker) attempt(logical int64, att int, shortDeadline bool) error {
	id := w.id*1_000_000 + logical*8 + int64(att)
	body := fmt.Sprintf("w=%d:r=%d:a=%d:pad=%x", w.id, logical, att, w.next()&0xffff)
	url := fmt.Sprintf("%s?%s", w.url, fmt.Sprintf("id=%d", id))

	base := context.Background()
	var cancel context.CancelFunc
	var ctx context.Context
	if shortDeadline {
		ctx, cancel = context.WithTimeout(base, 25*time.Millisecond)
	} else {
		ctx, cancel = context.WithCancel(base)
	}
	defer cancel()

	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) {
			if info.Reused {
				atomic.AddInt64(&reusedConns, 1)
			} else {
				atomic.AddInt64(&newConns, 1)
			}
		},
	}
	ctx = httptrace.WithClientTrace(ctx, trace)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		failf("scenario=%s worker=%d id=%d: build request: %v", scenario, w.id, id, err)
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := w.client.Do(req)
	if err != nil {
		cat, expected := classifyErr(err)
		if !expected {
			failf("scenario=%s worker=%d seed=%d id=%d attempt=%d: unclassified request error: %v",
				scenario, w.id, w.seed, id, att, err)
		}
		recordErr(cat)
		return fmt.Errorf("%s: %w", cat, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		cat, expected := classifyErr(err)
		if !expected {
			failf("scenario=%s worker=%d seed=%d id=%d body read: unclassified error: %v",
				scenario, w.id, w.seed, id, err)
		}
		recordErr(cat)
		return fmt.Errorf("%s: %w", cat, err)
	}
	if resp.StatusCode != http.StatusOK {
		failf("scenario=%s worker=%d seed=%d id=%d expected=HTTP 200 actual=%d body=%q",
			scenario, w.id, w.seed, id, resp.StatusCode, data)
	}
	var gotID int64
	var gotCRC uint32
	if _, err := fmt.Sscanf(string(data), "%d %d", &gotID, &gotCRC); err != nil {
		failf("scenario=%s worker=%d seed=%d id=%d malformed success body=%q: %v",
			scenario, w.id, w.seed, id, data, err)
	}
	want := crc32.ChecksumIEEE([]byte(body))
	if gotID != id || gotCRC != want {
		failf("scenario=%s worker=%d seed=%d id=%d checksum mismatch: got id=%d crc=%d want id=%d crc=%d",
			scenario, w.id, w.seed, id, gotID, gotCRC, id, want)
	}
	return nil
}

func (w *worker) request(logical int64) {
	var last error
	for att := 0; att < 3; att++ {
		if att > 0 {
			atomic.AddInt64(&retries, 1)
		}
		atomic.AddInt64(&attempts, 1)
		// 每 13 个逻辑请求用一次短 deadline；其余正常路径。
		short := (logical+int64(w.id))%13 == 0
		last = w.attempt(logical, att, short)
		if last == nil {
			atomic.AddInt64(&oks, 1)
			atomic.AddInt64(&beats, 1)
			return
		}
	}
	// 三次尝试可能全部命中 server 异常路径或 cancel；只计数，不做墙钟断言。
	atomic.AddInt64(&logicalFail, 1)
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
		w.request(r)
		r++
	}
}

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 8; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i)
			sink = append(sink[:0], b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		time.Sleep(20 * time.Millisecond)
	}
}

func watchdog(interval time.Duration, stop <-chan struct{}) {
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
				failf("scenario=%s watchdog stalled: no worker progress for %s (beats=%d)",
					scenario, time.Since(lastChange), cur)
			}
		}
	}
}

func waitGoroutines(base int, grace time.Duration) int {
	giveUp := time.Now().Add(grace)
	for {
		n := runtime.NumGoroutine()
		if n <= base+4 || time.Now().After(giveUp) {
			return n
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 8, 1, 64)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 32, 1, 100000))

	baseGoroutines := runtime.NumGoroutine()

	srv := httptest.NewUnstartedServer(serverHandler(seed))
	srv.Config.ReadHeaderTimeout = 2 * time.Second
	srv.Config.ReadTimeout = 5 * time.Second
	srv.Config.WriteTimeout = 5 * time.Second
	srv.Config.IdleTimeout = 2 * time.Second
	srv.StartTLS()

	baseTr := srv.Client().Transport.(*http.Transport)
	tr := baseTr.Clone()
	tr.MaxIdleConns = maxInt(2, workers)
	tr.MaxIdleConnsPerHost = maxInt(2, workers/2+1)
	tr.IdleConnTimeout = 2 * time.Second
	if tr.TLSClientConfig != nil {
		tr.TLSClientConfig = tr.TLSClientConfig.Clone()
		tr.TLSClientConfig.ClientSessionCache = tls.NewLRUClientSessionCache(32)
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		n := atomic.AddInt64(&dialSeq, 1)
		h := uint64(n)*0x9e3779b97f4a7c15 + seed
		h ^= h >> 31
		if h%8 == 0 {
			// 随机 cancel-in-dial：拨号进行中取消派生 ctx。
			atomic.AddInt64(&dialChaos, 1)
			cctx, cancel := context.WithCancel(ctx)
			timer := time.AfterFunc(time.Duration(1+(n%4))*100*time.Microsecond, cancel)
			conn, err := dialer.DialContext(cctx, network, addr)
			timer.Stop()
			cancel()
			if err != nil {
				return nil, err
			}
			return conn, nil
		}
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		// 小比例新连接上设置一次很短的 SetDeadline：握手/首请求超过 25ms 即超时，
		// 定时器随即清除 deadline，避免污染后续池化复用。失败由客户端分类并重试。
		if h%8 == 3 {
			atomic.AddInt64(&shortDeadlineConns, 1)
			_ = conn.SetDeadline(time.Now().Add(25 * time.Millisecond))
			time.AfterFunc(25*time.Millisecond, func() { _ = conn.SetDeadline(time.Time{}) })
		}
		return conn, nil
	}

	client := &http.Client{Transport: tr}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds max_idle=%d\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb, tr.MaxIdleConns)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &worker{
			id:     int64(i),
			seed:   seed,
			client: client,
			url:    srv.URL + "/work",
			rng:    seed*10000 + uint64(i)*7919 + 1,
		}
		ws[i] = w
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(deadline, seconds, fixed)
		}()
	}

	stop := make(chan struct{})
	var helpers sync.WaitGroup
	helpers.Add(2)
	go gcChurn(stop, &helpers)
	go func() {
		defer helpers.Done()
		watchdog(time.Duration(hb)*time.Second, stop)
	}()

	wg.Wait()
	close(stop)
	helpers.Wait()

	client.CloseIdleConnections()
	srv.Close()

	n := waitGoroutines(baseGoroutines, 10*time.Second)
	if n > baseGoroutines+4 {
		failf("scenario=%s phase=shutdown expected=goroutines<=%d actual=%d: goroutine leak",
			scenario, baseGoroutines+4, n)
	}
	if atomic.LoadInt64(&oks) == 0 {
		failf("scenario=%s phase=final seed=%d expected=\"at least one verified success\" actual=0 (attempts=%d)",
			scenario, seed, atomic.LoadInt64(&attempts))
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: attempts=%d ok=%d logical_fail=%d retries=%d reused_conns=%d new_conns=%d dial_cancel=%d short_deadline_conns=%d server_served=%d server_hijacked=%d server_early_close=%d err_canceled=%d err_deadline=%d err_timeout=%d err_eof=%d err_reset=%d err_malformed=%d gc_churn=%d beats=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		atomic.LoadInt64(&attempts), atomic.LoadInt64(&oks), atomic.LoadInt64(&logicalFail),
		atomic.LoadInt64(&retries), atomic.LoadInt64(&reusedConns), atomic.LoadInt64(&newConns),
		atomic.LoadInt64(&dialChaos), atomic.LoadInt64(&shortDeadlineConns), atomic.LoadInt64(&served),
		atomic.LoadInt64(&hijacked), atomic.LoadInt64(&earlyout), atomic.LoadInt64(&errCanceled),
		atomic.LoadInt64(&errDeadline), atomic.LoadInt64(&errTimeout), atomic.LoadInt64(&errEOF),
		atomic.LoadInt64(&errReset), atomic.LoadInt64(&errMalformed), atomic.LoadInt64(&gcCycles),
		atomic.LoadInt64(&beats), m.HeapAlloc, m.HeapObjects, m.NumGC, n)
	fmt.Printf("panics=0 scenario=%s\n", scenario)
	fmt.Println("STRESS PASS n11_http_tls_conn")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
