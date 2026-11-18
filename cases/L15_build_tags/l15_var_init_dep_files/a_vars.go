package main

// 同一文件内无依赖的变量按声明顺序初始化；Second 依赖 First。
var (
	trace  []string
	Beta   = note("beta", 2)
	First  = note("first", 10)
	Second = note("second", First+5)
)
