//go:build !linux

package main

// 非 linux：后缀 _other 不是 GOOS，文件名自身不过滤，只靠 tag 约束。
func init() { tokens = append(tokens, "y-nonlinux") }

func impl() string { return "non-linux" }
