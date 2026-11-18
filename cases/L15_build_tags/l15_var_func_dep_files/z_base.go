package main

// A 在字典序最后的文件里声明，但函数依赖分析会让它最先初始化。
var A = note("A", 5)
