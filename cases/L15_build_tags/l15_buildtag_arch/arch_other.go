//go:build !amd64 && !arm64

package main

func archImpl() string { return "other" }
