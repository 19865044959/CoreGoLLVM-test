//go:build !linux

package main

// 非 linux 的互斥文件：与 b_linux_setup.go 占用同一个字典序槽位。
var VTag = note("var-tag-other", VA+19)

func init() { note("init-tag-other", 0) }
