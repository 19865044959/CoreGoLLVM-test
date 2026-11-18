package main

// 末段是 GOOS=linux，倒数第二段 amd64 不是 GOOS，因此按 _linux 后缀匹配：
// linux/amd64 与 linux/arm64 都会编译本文件。
func init() { mark("pair_amd64_linux") }
