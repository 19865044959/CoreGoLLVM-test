package main

func init() {
	// 最后一个 init：此时全部包级变量都必须已初始化。
	initSawAllValues = VA == 1 && VM == VTag+1 && payload != nil && payload.v == VM
	note("init-z", 0)
}
