//go:build !amd64

package main

// 成对后缀 _linux_amd64 要求 linux+amd64，而约束又要求 !amd64：
// 交集为空，任何平台都不编译。
func init() { tokens = append(tokens, "q-contradiction") }

func impl() string { return "pair-on-not-amd64" }
