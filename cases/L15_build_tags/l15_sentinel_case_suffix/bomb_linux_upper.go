//go:build Linux

package main

// 内建 GOOS tag 是小写 linux；大写 Linux 是另一个未设置的 tag，文件必须被排除。
func marker() string { return "bomb-Linux" }

func broken( {

func init() { tokens = append(tokens, "bomb-Linux") }
