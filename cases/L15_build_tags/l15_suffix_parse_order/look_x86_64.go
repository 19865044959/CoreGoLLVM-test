package main

// 末段 64 不是合法的 GOARCH（amd64 才是），因此是普通文件。
func init() { mark("look_x86_64") }
