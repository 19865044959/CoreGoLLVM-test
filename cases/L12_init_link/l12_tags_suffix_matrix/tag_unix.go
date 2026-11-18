//go:build unix

package main

// Compound build constraint using Go's unix meta tag.
var tagUnixVar = addVar("unix.var")

func init() {
	addInit("unix.init")
}
