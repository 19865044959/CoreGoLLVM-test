//go:build !amd64

package b

// 非 amd64：b 依赖 a；a 的后缀文件在非 amd64 不导入 b，import 图无环。
import "l15_cycle_goarch_edges/a"

func Edge() string { return "b->" + a.A }
