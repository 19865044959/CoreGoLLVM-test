package main

// Blank imports are the subject of this case. Each imported package performs
// global registrations in its own init function before this package's
// package-level variables are initialized.
import (
	_ "image/png"
	_ "net/http/pprof"
)
