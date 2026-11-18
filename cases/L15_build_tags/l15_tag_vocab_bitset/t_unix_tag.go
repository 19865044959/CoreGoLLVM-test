//go:build unix

package main

// unix 是元标签：在 linux/darwin/freebsd 等所有类 Unix GOOS 上成立。
func init() { recordToken("meta-unix") }
