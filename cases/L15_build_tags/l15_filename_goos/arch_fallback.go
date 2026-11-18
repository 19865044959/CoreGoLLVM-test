//go:build !(linux && amd64)

package main

func archSuffixImpl() string { return "fallback" }
