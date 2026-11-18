//go:build linux

package main

// 文件名后缀 _linux 与 //go:build linux 同时成立才编译本文件。
func impl() string { return "linux" }

func init() { mark("linux") }
