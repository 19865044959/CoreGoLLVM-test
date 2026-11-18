package main

import "testing"

// 测试文件不参与 go build；这里重复定义 impl 作为"被错误纳入"的哨兵。
func impl() string { return "test-dup" }

func init() { recordToken("test-token") }

func TestIgnoredByBuild(t *testing.T) {
	if impl() != "test-dup" {
		t.Fatal("unreachable")
	}
}
