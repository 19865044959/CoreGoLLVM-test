//go:build windows

package main

var windowsMark = addMark("windows.var")

func init() {
	addMark("windows.init")
	_ = windowsMark
}
