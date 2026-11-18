package main

// 普通 go build 不解析 _test.go 的包体：这里的语法错误、重复符号与 init
// 都不应被看到。只有显式构建测试二进制时才会解析测试文件。
func baseImpl() string { return "test-file" }

func broken( {

func init() { baseInits += 100 }
