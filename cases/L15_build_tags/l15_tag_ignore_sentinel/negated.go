//go:build !ignore && !true && !false

package main

// 三个哨兵都是未设置的普通 tag，取反后为真：本文件必须参与编译。
// 若编译器把 true/false 当布尔字面量，本文件会被排除并导致构建失败。
var sentinelNeg = "neg-ok"
