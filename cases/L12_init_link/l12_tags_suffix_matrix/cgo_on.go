//go:build cgo

package main

// Selected when CGO_ENABLED resolves the cgo tag true.
var cgoOnVar = addVar("cgo.var")

func init() {
	addInit("cgo.init")
}
