package main

// viaFunc 的函数体引用 A：B 依赖 A，即使 A 声明在 z_base.go（字典序最后）。
func viaFunc() int64 { return A }

var B = note("B", viaFunc()+1)
