// Package boom is imported for side effects. Its init behavior is selected by
// L12_INIT_CHILD so the parent test binary can re-exec itself and observe how
// failures in a blank-imported package's init affect the process.
package boom

import "os"

// Mode is captured before init runs.
var Mode = os.Getenv("L12_INIT_CHILD")

func init() {
	switch Mode {
	case "panic":
		panic("nested init boom")
	case "nil":
		panic(nil)
	case "deferpanic":
		// Runs while init is returning normally, after main would have been
		// reached without the deferred panic.
		defer func() {
			panic("defer boom")
		}()
	case "deferexit":
		// Exits with status 0 before main starts and without a pass marker.
		defer os.Exit(0)
	case "recover":
		defer func() {
			_ = recover()
		}()
		panic("child recovered")
	case "exit7":
		os.Exit(7)
	}
}
