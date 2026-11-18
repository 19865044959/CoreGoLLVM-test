//go:build !windows

package main

var notWindowsMark = addMark("notwindows.var")

func init() {
	addMark("notwindows.init")
	_ = notWindowsMark
}
