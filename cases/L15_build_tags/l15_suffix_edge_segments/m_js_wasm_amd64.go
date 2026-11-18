package main

// 倒数第二段 wasm 是 GOARCH 而不是 GOOS：不成对，仅末段 amd64 匹配。
func init() { tokens = append(tokens, "M_JS_WASM_AMD64") }
