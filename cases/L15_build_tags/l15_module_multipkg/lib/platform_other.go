//go:build !linux

package lib

func Platform() string { return "other-lib" }
