// L12: duplicate registration detection with both error and panic contracts.
// The registry must reject duplicates atomically (no partial update), reject
// registrations after freeze, and canonicalize names case-insensitively.
// A route table models net/http.ServeMux-style duplicate-pattern panics.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
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

func main() {
	seedVal = readSeed()
	seedStr = strconv.FormatInt(seedVal, 10)

	// Builtins came from two init functions; their relative order is undefined.
	ck(plugins.count() == 3, "three builtins registered in init")
	ck(strings.Join(plugins.names(), ",") == "gob,json,text", "sorted builtin names")
	jsonPlugin := plugins.lookup("json")
	ck(jsonPlugin != nil && jsonPlugin.factor == 2 && jsonPlugin.data[0] == 2, "json builtin payload")

	// Duplicate names are detected case-insensitively and never overwrite.
	got, err := plugins.add("json", 99)
	ck(errors.Is(err, errDuplicate) && got == nil, "duplicate json rejected")
	ck(plugins.lookup("json") == jsonPlugin && jsonPlugin.factor == 2 && plugins.count() == 3,
		"duplicate registration did not overwrite")
	_, err = plugins.add("JSON", 100)
	ck(errors.Is(err, errDuplicate), "case-insensitive duplicate rejected")
	_, err = plugins.add("", 1)
	ck(errors.Is(err, errEmptyName), "empty name rejected")
	ck(plugins.count() == 3, "failed adds leave table unchanged")

	// A fresh name succeeds; a different-case duplicate still fails.
	yaml, err := plugins.add("yaml", 4)
	ck(err == nil && yaml != nil && yaml.factor == 4, "yaml registered")
	_, err = plugins.add("YAML", 5)
	ck(errors.Is(err, errDuplicate), "duplicate yaml rejected")
	ck(plugins.count() == 4 && plugins.lookup("yaml") == yaml, "yaml table state")

	// Freeze takes precedence for all further additions, including duplicates.
	plugins.freeze()
	_, err = plugins.add("xml", 6)
	ck(errors.Is(err, errFrozen), "frozen registry rejects new name")
	_, err = plugins.add("json", 7)
	ck(errors.Is(err, errFrozen), "frozen registry rejects even duplicates")
	ck(plugins.count() == 4 && plugins.lookup("xml") == nil, "frozen table unchanged")

	// Panic contract for duplicate routes: the first handler stays installed.
	var hitA, hitB int32
	hA := func(int) int { return int(atomic.AddInt32(&hitA, 1)) }
	hB := func(int) int { return int(atomic.AddInt32(&hitB, 1)) }
	routes.handle("/a", hA)
	rp, other := catchRoutePanic(func() { routes.handle("/a", hB) })
	ck(rp != nil && other == nil, "duplicate route panics with routePanic")
	if rp != nil {
		ck(rp.pattern == "/a" && rp.kind == "duplicate route", "panic carries pattern and kind")
		ck(strings.Contains(rp.Error(), "/a"), "panic message contains pattern")
	}
	ck(len(routes.m) == 1 && routes.m["/a"] != nil, "duplicate route table unchanged")
	routes.m["/a"](0)
	ck(atomic.LoadInt32(&hitA) == 1 && atomic.LoadInt32(&hitB) == 0,
		"first route handler still active")

	// Nil handler is rejected before insertion.
	_, other = catchRoutePanic(func() { routes.handle("/nil", nil) })
	ck(errors.Is(other, errNilHandler), "nil handler panic uses sentinel error")
	ck(len(routes.m) == 1, "nil handler did not insert a route")

	// The table is usable again after recovering from a panic.
	rp, other = catchRoutePanic(func() { routes.handle("/b", hB) })
	ck(rp == nil && other == nil && len(routes.m) == 2 && routes.m["/b"] != nil,
		"registration recovers after panic")

	// Rotate reads by seed; then GC and re-check all live objects.
	start := int(seedVal % 4)
	for i := 0; i < 8; i++ {
		switch (start + i) % 4 {
		case 0:
			ck(plugins.lookup("json") == jsonPlugin, "rotating plugin check")
		case 1:
			ck(plugins.lookup("yaml") == yaml, "rotating plugin check")
		case 2:
			ck(routes.m["/a"] != nil, "rotating route check")
		case 3:
			ck(routes.m["/b"] != nil, "rotating route check")
		}
	}

	gcStress()
	ck(plugins.lookup("json") == jsonPlugin && jsonPlugin.data[31] == byte(2+31),
		"plugin pointer and payload survive GC")
	ck(plugins.lookup("yaml") == yaml && yaml.data[31] == byte(4+31), "yaml survives GC")
	ck(len(routes.m) == 2 && routes.m["/a"] != nil && routes.m["/b"] != nil,
		"routes survive GC")
	ck(plugins.count() == 4 && strings.Join(plugins.names(), ",") == "gob,json,text,yaml",
		"registry contents stable after GC")

	finish("l12_dup_register_detect")
}
