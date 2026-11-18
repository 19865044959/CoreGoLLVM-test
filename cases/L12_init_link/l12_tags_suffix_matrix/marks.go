package main

import (
	"runtime"
	"sort"
	"sync/atomic"
)

// varMarks and initMarks have no initializers, so individual file var
// initializers can append to them in any file order.
var (
	varMarks  []string
	initMarks []string
	phaseOK   int32
)

func addVar(s string) string {
	varMarks = append(varMarks, s)
	return s
}

func addInit(s string) {
	initMarks = append(initMarks, s)
}

func isUnix(goos string) bool {
	switch goos {
	case "aix", "android", "darwin", "dragonfly", "freebsd", "hurd",
		"illumos", "ios", "linux", "netbsd", "openbsd", "solaris":
		return true
	}
	return false
}

// expectedStems enumerates only the platform-specific stems selected for the
// current GOOS/GOARCH. The cgo pair is handled separately because whether the
// cgo build tag is set depends on CGO_ENABLED, not on runtime.GOOS.
func expectedStems() []string {
	var out []string
	if runtime.GOOS == "linux" {
		out = append(out, "suffix_linux")
	}
	if runtime.GOARCH == "amd64" {
		out = append(out, "suffix_amd64")
	}
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		out = append(out, "suffix_linux_amd64")
	}
	if isUnix(runtime.GOOS) {
		out = append(out, "unix")
	}
	if runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64" {
		out = append(out, "pair")
	}
	if runtime.GOOS == "windows" {
		out = append(out, "suffix_windows")
	}
	if runtime.GOARCH == "386" {
		out = append(out, "suffix_386")
	}
	sort.Strings(out)
	return out
}

func init() {
	// Every package-level variable initializer has completed before any init
	// function runs; this is the phase boundary the case checks.
	if len(varMarks) == len(expectedStems())+1 {
		atomic.StoreInt32(&phaseOK, 1)
	}
}
