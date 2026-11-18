//go:build unix && (386 || arm || mips || mipsle || ppc || s390)

package main

func init() { classInits = append(classInits, "unix32") }

func platformClass() string { return "unix32" }

func platformScale() int64 { return 2 }
