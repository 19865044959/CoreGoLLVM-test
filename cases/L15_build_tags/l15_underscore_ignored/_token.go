package main

// 若该文件被错误编译，构建仍会成功，但 token 数量断言会失败。
func init() { recordToken("underscore-token") }
