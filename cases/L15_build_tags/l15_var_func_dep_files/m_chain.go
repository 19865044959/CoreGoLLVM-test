package main

// chainC 的函数体引用 B：C 依赖 B，初始化顺序为 A→B→C。
func chainC() int64 { return B * 2 }

var C = note("C", chainC()+1)
