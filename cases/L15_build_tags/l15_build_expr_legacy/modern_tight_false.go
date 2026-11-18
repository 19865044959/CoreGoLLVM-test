//go:build !((linux)&&(amd64))

package main

func modernTight() string { return "no" }
