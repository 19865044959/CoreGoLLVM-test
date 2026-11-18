package main

// 末两段是 amd64/extra，不构成 GOOS_GOARCH 后缀，按普通文件编译。
func init() { suffixTokens = append(suffixTokens, "ordinary-extra") }
