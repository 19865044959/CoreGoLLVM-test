//go:build linux && !tagFast

package main

func suffixImpl() string { return "linux-slow" }
