//go:build !Ignore && !TRUE && !False && !Linux

package main

// 四个未设置哨兵取反后恒真：本文件必须参与编译。
const negCase = "neg-ok"

func init() { tokens = append(tokens, "neg-case") }
