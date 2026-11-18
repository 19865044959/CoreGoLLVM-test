//go:build windows

package main

// 后缀 _linux 与约束 windows 矛盾：任何平台都不应编译本文件。
// 如果实现把后缀与 tag 当成并集，windows 构建会选中它并造成重复符号。
func init() { tokens = append(tokens, "z-contradiction") }

func impl() string { return "windows-on-linux-suffix" }
