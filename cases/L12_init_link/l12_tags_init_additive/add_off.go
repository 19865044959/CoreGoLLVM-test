//go:build l12_additive_off

package main

// This file must be excluded because the custom tag is not enabled for the
// default build. Its marks must never appear.

var offMark = addMark("off.var")

func init() {
	addMark("off.init")
	_ = offMark
}
