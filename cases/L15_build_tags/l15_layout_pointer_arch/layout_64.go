//go:build !386 && !arm && !mips && !mipsle

package main

// 64 位平台被 tag 选中的布局：ID 在前，指针 Ref 在后（偏移 8）。
// 与 32 位版本字段顺序相反、大小不同，但都是合法的 Go 结构体。
type packed struct {
	ID  int
	Ref *blob
}

const layoutName = "ptr-second"
