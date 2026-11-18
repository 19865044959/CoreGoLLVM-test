package main

// 文件名里的 false 不是特殊后缀关键字，文件照常编译。
const falseNameOK = "false-name"

func init() { tokens = append(tokens, "name-false") }
