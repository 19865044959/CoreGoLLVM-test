//go:build !linux

package b

// 非 linux 下 b 依赖 a；a 的非 linux 文件不导入 b，因此没有环。
import "l15_tag_cycle_break/a"

func Helper() string { return "b->" + a.A }
