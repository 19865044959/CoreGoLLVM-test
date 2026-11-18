//go:build !((tagA && !tagA) || (tagB && !tagB))

package main

// 恒真表达式：对两个矛盾式的 OR 取反；只有 NOT/括号解析正确时才会编译。
func impossibleImpl() string { return "possible" }
