//go:build !linux && !windows && !darwin

package main

func osSuffixImpl() string { return "other" }
