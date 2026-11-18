//go:build !(linux && amd64)

package main

func dualArc() string { return "fallback" }
