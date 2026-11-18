//go:build ignore

package main

// The ignore tag excludes this file from normal package builds.
var tagIgnoreVar = addVar("ignore.var")

func init() {
	addInit("ignore.init")
}
