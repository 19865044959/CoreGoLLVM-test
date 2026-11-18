package main

// 末段 custom 不是 GOARCH，整个文件名不匹配 GOOS_GOARCH 规则，因此是普通文件。
func init() { suffixTokens = append(suffixTokens, "ordinary-linux-custom") }
