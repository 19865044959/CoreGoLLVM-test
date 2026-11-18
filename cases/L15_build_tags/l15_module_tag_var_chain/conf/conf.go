package conf

import "l15_module_tag_var_chain/trace"

type Item struct {
	ID    int64
	Label string
	Next  *Item
}

// Base/Node 的包级变量初始化器读取 tag 选择的常量。
var Base = PlatformBias * 2

var Node = &Item{ID: PlatformBias, Label: PlatformName}

func init() {
	Node.Next = &Item{ID: Base, Label: PlatformName + "-next"}
	trace.Mark("conf")
}
