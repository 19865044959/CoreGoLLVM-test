//go:build l12_suffix_off

package main

// Must be excluded: the custom tag is not enabled and GOOS is not windows.
var tagOffVar = addVar("off.var")

func init() {
	addInit("off.init")
}
