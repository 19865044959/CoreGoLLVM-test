package main

// rect 的方法全部为指针接收者（分布在两个文件中），
// 因此只有 *rect 满足 Shape。
type rect struct {
	w, h int64
}
