//go:build !cgo

package main

// Selected when CGO_ENABLED resolves the cgo tag false.
var cgoOffVar = addVar("nocgo.var")

func init() {
	addInit("nocgo.init")
}
