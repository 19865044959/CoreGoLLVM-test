//go:build linux

package main

var linuxMark = addMark("linux.var")

func init() {
	addMark("linux.init")
	_ = linuxMark
}
