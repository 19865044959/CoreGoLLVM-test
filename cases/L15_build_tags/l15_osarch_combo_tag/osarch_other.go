//go:build !(linux && arm64) && !(windows && amd64) && !(darwin && arm64) && !(freebsd && amd64) && !((linux || freebsd) && 386)

package main

func osArchImpl() string { return "other" }
