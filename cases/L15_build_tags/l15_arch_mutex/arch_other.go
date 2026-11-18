//go:build !amd64 && !386 && !arm64

package main

func init() { archInits = append(archInits, "other") }

func archMutImpl() string { return "other" }
