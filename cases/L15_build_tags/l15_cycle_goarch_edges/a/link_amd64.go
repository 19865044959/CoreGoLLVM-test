package a

// 文件名后缀 _amd64：仅 amd64 参与编译。此时 a 依赖 b，
// 而 b 的 amd64 文件不导入 a，import 图无环。
import "l15_cycle_goarch_edges/b"

func Edge() string { return "a->" + b.Name }
