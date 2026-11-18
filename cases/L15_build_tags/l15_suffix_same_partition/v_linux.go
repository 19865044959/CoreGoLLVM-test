//go:build linux && amd64

package main

// 后缀 _linux 要求 linux，约束再把同一后缀的文件划分到 amd64。
func init() { tokens = append(tokens, "v-linux-amd64") }

func impl() string { return "linux-amd64" }
