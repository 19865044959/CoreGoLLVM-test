package main

// 386 是 GOARCH 而不是 GOOS：不成对，仅末段 amd64 参与匹配。
func init() { tokens = append(tokens, "M_386_AMD64") }
