package main

// 末段 linux，arm（GOARCH）位于中间，不参与过滤。
func init() { tokens = append(tokens, "M_ARM_LINUX") }
