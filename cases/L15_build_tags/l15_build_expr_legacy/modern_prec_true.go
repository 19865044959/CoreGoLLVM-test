//go:build linux && amd64 || windows

package main

// && 优先于 ||：(linux && amd64) || windows。
func modernPrec() string { return "hit" }
