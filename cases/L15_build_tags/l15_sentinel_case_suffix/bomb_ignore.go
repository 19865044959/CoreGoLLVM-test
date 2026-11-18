//go:build Ignore

package main

// 大写 Ignore 是另一个（未设置的）tag 名，不能折叠成 ignore；本文件必须被排除。
func marker() string { return "bomb-Ignore" }

func broken( {

func init() { tokens = append(tokens, "bomb-Ignore") }
