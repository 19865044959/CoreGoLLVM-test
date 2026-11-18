//go:build !(linuxx || unixx || go1 || gc1 || go1.24x || Linux || amd64x)

package main

// 上一文件的严格补集：默认构建必须命中本文件而不是 lookalike 文件。
func init() { recordToken("lookalike-absent") }
