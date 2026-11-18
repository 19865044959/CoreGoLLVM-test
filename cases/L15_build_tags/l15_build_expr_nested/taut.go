//go:build (linux || !linux) && (!windows || windows)

package main

// 恒真表达式：括号 + OR + NOT 的组合解析必须成立。
func tautImpl() string { return "taut" }
