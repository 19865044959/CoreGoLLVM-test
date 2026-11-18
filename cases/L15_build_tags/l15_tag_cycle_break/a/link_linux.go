//go:build linux

package a

// linux 下 a 依赖 b；b 的 linux 文件不导入 a，因此没有环。
import "l15_tag_cycle_break/b"

func Partner() string { return "a->" + b.Name }
