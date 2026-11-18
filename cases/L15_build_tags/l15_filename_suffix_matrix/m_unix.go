package main

// unix 不是合法 GOOS 值：文件名后缀规则不认识它（与 //go:build unix 不同），
// 因此本文件是无约束的普通文件。
func init() { suffixTokens = append(suffixTokens, "ordinary-unix") }
