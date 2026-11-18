//go:build 386 || arm || mips || mipsle

package main

// 32 位平台被 tag 选中的布局：指针 Ref 在前，ID 在后（偏移 4）。
// 字段顺序与 64 位版本相反，GC 必须按被选中布局的指针偏移扫描。
type packed struct {
	Ref *blob
	ID  int
}

const layoutName = "ptr-first"
