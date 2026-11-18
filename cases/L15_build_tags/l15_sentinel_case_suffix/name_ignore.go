package main

// 文件名里的 ignore 不是特殊后缀关键字，文件照常编译。
const ignoreNameOK = "ignore-name"

func init() { tokens = append(tokens, "name-ignore") }
