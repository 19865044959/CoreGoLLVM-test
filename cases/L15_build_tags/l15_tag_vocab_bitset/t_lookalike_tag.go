//go:build linuxx || unixx || go1 || gc1 || go1.24x || Linux || amd64x

package main

// 形似真实标签的自定义标签，工具链不会设置；Linux 大小写也不同。
func init() { recordToken("lookalike") }
