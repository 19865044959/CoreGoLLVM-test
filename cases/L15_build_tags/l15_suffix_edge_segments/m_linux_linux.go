package main

// 末段是 GOOS linux，倒数第二段也是 linux：按单段匹配，linux 下编译。
func init() { tokens = append(tokens, "M_LINUX_LINUX") }
