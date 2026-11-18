package main

// 变量初始化器先于任何 init 执行；同一文件内的多个 init 按源码顺序。
var VA = note("var-a", 1)

func init() { note("init-a1", 0) }

func init() { note("init-a2", 0) }
