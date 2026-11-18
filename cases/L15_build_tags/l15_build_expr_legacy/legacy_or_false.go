// +build !linux
// +build !darwin

package main

// 多行 +build = AND：仅当既非 linux 也非 darwin 时命中。
func legacyOR() string { return "plain" }
