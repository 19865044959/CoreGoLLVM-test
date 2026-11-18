//go:build !(linux && amd64 || windows)

package main

func modernPrec() string { return "miss" }
