//go:build linux && amd64

package main

// 后缀 _arm64 与约束 amd64 的交集恒为空：linux/amd64 上后缀不匹配，
// linux/arm64 上约束不匹配，其余平台两者都不匹配。
func impl() string { return "bad-arm64" }

func init() { mark("bad-arm64") }
