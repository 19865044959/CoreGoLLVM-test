// +build !linux !amd64

package main

// 空格分隔 = OR，是 linux,amd64 的严格补集。
func legacyCombo() string { return "other" }
