//go:build linux && !amd64 && !arm64

package main

// 同一后缀下的兜底分支，覆盖 linux 的其余 GOARCH。
func init() { tokens = append(tokens, "x-linux-other") }

func impl() string { return "linux-other" }
