//go:build arm64

package main

func init() { archInits = append(archInits, "arm64") }

func archMutImpl() string { return "arm64" }
