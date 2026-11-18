//go:build linux && (!windows || amd64)

package main

// NOT 作用于括号分组，再与 AND 组合（Go 不允许 !! 双重否定）。
func modernGroup() string { return "grouped" }
