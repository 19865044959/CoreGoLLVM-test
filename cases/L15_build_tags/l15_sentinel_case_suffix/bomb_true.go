//go:build TRUE

package main

// 大写 TRUE 是普通 tag 名，不是布尔字面量 true；文件必须被排除。
func marker() string { return "bomb-TRUE" }

func broken( {

func init() { tokens = append(tokens, "bomb-TRUE") }
