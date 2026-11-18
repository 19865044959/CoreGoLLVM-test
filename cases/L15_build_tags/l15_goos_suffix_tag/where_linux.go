//go:build tagFast

package main

// 文件名 _linux 隐含 GOOS=linux，与 tagFast 约束取交集：
// 只有 linux + tagFast 构建才会包含本文件。
func suffixImpl() string { return "linux-fast" }
