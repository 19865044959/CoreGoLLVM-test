// L12: side effects of blank-imported subpackages. The importing package's
// package-level variables must observe the imported packages' init effects:
// image/png registers the PNG decoder and net/http/pprof registers handlers
// on the global DefaultServeMux.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	seedVal int64
	seedStr string

	// Evaluated after all imported packages have run their init functions and
	// before main.init. Both must already be true.
	pngFormatAtInit    = pngVisible()
	pprofPatternAtInit = pprofPattern("/debug/pprof/")
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

func checkFormats() {
	ck(pngVisible(), "image/png registered its decoder")
	ck(jpegLooksUnregistered(), "unimported image/jpeg decoder is absent")

	cfg, format, err := decodeConfigFixture()
	ck(err == nil && format == "png", "blank-imported PNG decoder is used")
	ck(cfg.Width == 1 && cfg.Height == 1, "decoded PNG dimensions")

	img, format, err := decodeFixture()
	ck(err == nil && format == "png", "full PNG decode")
	if err == nil && img != nil {
		b := img.Bounds()
		r, g, bl, a := img.At(0, 0).RGBA()
		ck(b.Dx() == 1 && b.Dy() == 1, "image bounds")
		ck(r == 0xffff && g == 0 && bl == 0 && a == 0xffff, "decoded pixel is opaque red")
	}
}

func checkPprof() {
	paths := []string{
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
		"/debug/pprof/trace",
		"/debug/pprof/goroutine",
	}
	for _, path := range paths {
		ck(strings.Contains(pprofPattern(path), "/debug/pprof"), "pprof pattern registered: "+path)
	}
	ck(pprofPattern("/not/registered") == "", "unrelated path has no pprof handler")

	status, body := pprofIndex()
	ck(status == 200, "pprof index handler status")
	ck(strings.Contains(body, "Types of profiles available"), "pprof index body")
}

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	ck(pngFormatAtInit, "PNG format visible in package-level variable initialization")
	ck(strings.Contains(pprofPatternAtInit, "/debug/pprof/"),
		"pprof handlers visible in package-level variable initialization")
	checkFormats()
	checkPprof()

	// Concurrent use of the blank-imported decoder, with GC in the middle.
	const workers = 8
	const iters = 200
	start := int(seedVal % 4)
	var wg sync.WaitGroup
	var bad int32
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				img, format, err := decodeFixture()
				if err != nil || format != "png" || img == nil {
					atomic.AddInt32(&bad, 1)
					return
				}
				if img.Bounds().Dx() != 1 || (start+w+i)%4 == 0 {
					if !strings.Contains(pprofPattern("/debug/pprof/"), "/debug/pprof") {
						atomic.AddInt32(&bad, 1)
						return
					}
				}
			}
		}(w)
	}
	for i := 0; i < 4; i++ {
		for j := 0; j < 8; j++ {
			p := make([]byte, 8192)
			p[j] = byte(i)
			gcSink = append(gcSink[:0], p)
		}
		runtime.GC()
	}
	wg.Wait()
	ck(atomic.LoadInt32(&bad) == 0, "concurrent decode and pprof lookup under GC")
	gcSink = gcSink[:0]

	gcStress()
	ck(pngVisible(), "PNG registration survives GC")
	ck(strings.Contains(pprofPattern("/debug/pprof/"), "/debug/pprof"),
		"pprof registration survives GC")
	checkFormats()
	checkPprof()

	finish("l12_blank_import_sideeffect")
}
