package main

// 叶子层：依赖 vars_mid.go / vars_root.go 的变量，声明顺序与初始化顺序相反。
var a = b + 1
var derived2 = derived1*2 + c
var fnInit = scale(derived1) + b
var table = map[int]string{
	a:        "seven",
	derived1: "hundred-one",
}
