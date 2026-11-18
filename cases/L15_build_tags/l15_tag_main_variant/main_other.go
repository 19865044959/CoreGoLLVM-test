//go:build !linux && !windows

package main

func main() { runMain("other", 3) }
