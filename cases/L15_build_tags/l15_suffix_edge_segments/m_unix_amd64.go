package main

// unix 不是 GOOS：末段 amd64 按单段匹配。
func init() { tokens = append(tokens, "M_UNIX_AMD64") }
