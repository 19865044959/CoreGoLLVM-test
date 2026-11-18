//go:build ignore

package main

// The ignore tag is never set by the go tool; this file must be excluded.

var ignoreMark = addMark("ignore.var")

func init() {
	addMark("ignore.init")
	_ = ignoreMark
}
