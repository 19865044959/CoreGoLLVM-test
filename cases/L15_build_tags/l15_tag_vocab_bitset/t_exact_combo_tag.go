//go:build linux && amd64 && unix && gc && go1.24 && !gccgo && !windows

package main

// 七个真实标签的合取；只有 linux/amd64 + gc + go1.24 构建命中。
func init() { recordToken("exact-combo") }
