package main

// 三段：amd64 位于中间（不是 GOOS），只有末段 linux 参与匹配。
func init() { tokens = append(tokens, "M_LINUX_AMD64_LINUX") }
