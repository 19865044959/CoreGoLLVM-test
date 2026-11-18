//go:build !linux

package main

func platformImpl() string { return "other-file" }
