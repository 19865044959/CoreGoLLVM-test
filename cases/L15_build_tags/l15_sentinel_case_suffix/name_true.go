package main

// 文件名里的 true 不是特殊后缀关键字，文件照常编译。
const trueNameOK = "true-name"

func init() { tokens = append(tokens, "name-true") }
