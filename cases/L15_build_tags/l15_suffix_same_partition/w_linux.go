//go:build linux && arm64

package main

// 与 v_linux.go 同为 _linux 后缀，由 tag 排除 amd64。
func init() { tokens = append(tokens, "w-linux-arm64") }

func impl() string { return "linux-arm64" }
