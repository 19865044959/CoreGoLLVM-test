//go:build unix && (amd64 || arm64 || ppc64 || ppc64le || mips64 || mips64le || riscv64 || s390x || loong64)

package main

func init() { classInits = append(classInits, "unix64") }

func platformClass() string { return "unix64" }

func platformScale() int64 { return 3 }
