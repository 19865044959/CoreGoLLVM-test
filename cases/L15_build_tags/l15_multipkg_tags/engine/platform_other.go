//go:build !linux

package engine

func platformName() string { return "other-engine" }
