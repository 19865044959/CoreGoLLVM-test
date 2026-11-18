//go:build linux && amd64

package main

// 后缀组合 _linux_amd64 与同义约束取交集，只在 linux/amd64 编译。
func dualArc() string { return "suffix+tag:linux-amd64" }
