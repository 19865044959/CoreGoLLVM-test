//go:build windows

package main

func init() { classInits = append(classInits, "windows") }

func platformClass() string { return "windows" }

func platformScale() int64 { return 4 }
