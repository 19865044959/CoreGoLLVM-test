package main

// 跨文件依赖：Third 依赖 a_vars.go 的 Second。
var (
	Alpha = note("alpha", 3)
	Third = note("third", Second+5)
)
