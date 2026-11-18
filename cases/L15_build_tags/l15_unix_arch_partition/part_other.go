//go:build !(unix && (amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64)) && !(unix && (386 || arm || mips || mipsle || ppc || s390)) && !windows

package main

func init() { classInits = append(classInits, "other") }

func platformClass() string { return "other" }

func platformScale() int64 { return 1 }
