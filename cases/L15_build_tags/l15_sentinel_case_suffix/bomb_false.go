//go:build False

package main

// 大写 False 是普通 tag 名，不是布尔字面量 false；文件必须被排除。
func marker() string { return "bomb-False" }

func broken( {

func init() { tokens = append(tokens, "bomb-False") }
