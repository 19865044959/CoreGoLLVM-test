//go:build linux

package main

// 后缀 _windows 与约束 linux 的交集恒为空：任何平台都不会编译本文件。
// 若编译器把文件名后缀与 build tag 当成 OR，本文件会与 impl_linux.go 冲突。
func impl() string { return "bad-windows" }

func init() { mark("bad-windows") }
