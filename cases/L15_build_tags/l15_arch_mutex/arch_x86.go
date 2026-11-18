//go:build amd64 || 386

package main

func init() { archInits = append(archInits, "x86") }

func archMutImpl() string { return "x86" }
