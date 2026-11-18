package main

// 倒数第二段 amd64 是 GOARCH 而不是 GOOS：不成对，仅末段 386 匹配。
func init() { tokens = append(tokens, "M_LINUX_AMD64_386") }
