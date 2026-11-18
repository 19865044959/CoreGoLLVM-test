package hub

import "l15_tag_import_graph/trace"

// Platform/Scale/Bias 的初始化器定义在 tag 选择的 route 文件中：
// 构建标签决定 import 图，被导入的 leaf 包必须先完成 init。
var Platform, Scale = platformInfo()

var Bias = platformBias()

func init() { trace.Mark("hub") }

func Combine(id int64) int64 { return id*Scale + Bias }

func NodeRef() any { return platformNodeAny() }
