//go:build linux

package main

// 文件名排在 m_vars.go 之前，但依赖 z_vars.go 的 Fourth：依赖优先于文件顺序。
var (
	Tagged     = note("tagged", Fourth+5)
	TaggedTail = Tagged + 1
)
