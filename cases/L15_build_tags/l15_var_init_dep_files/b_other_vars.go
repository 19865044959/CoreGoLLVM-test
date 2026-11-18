//go:build !linux

package main

// 非 linux 的 tag 分支，同样依赖 z_vars.go 的 Fourth。
var (
	Tagged     = note("tagged-other", Fourth+7)
	TaggedTail = Tagged + 1
)
