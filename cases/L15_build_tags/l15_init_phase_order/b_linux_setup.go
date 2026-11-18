//go:build linux

package main

// 文件名排在 a 与 m 之间：变量在变量阶段按文件序参与，init 在 init 阶段插槽。
var VTag = note("var-tag-linux", VA+9)

func init() { note("init-tag-linux", 0) }
