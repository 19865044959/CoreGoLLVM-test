package main

// 目录名以 .go 结尾，但目录不是源文件；go build . 不会递归编译它。
func impl() string { return "bad-directory" }

func broken( {
