//go:build linux && amd64

package main

var linuxAmd64Mark = addMark("linux_amd64.var")

func init() {
	addMark("linux_amd64.init")
	_ = linuxAmd64Mark
}
