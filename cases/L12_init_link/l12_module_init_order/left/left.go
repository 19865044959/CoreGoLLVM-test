// Package left is one of two independent branches below top.
package left

import (
	"l12_module_init_order/base"
	"l12_module_init_order/sink"
)

var Left = base.Base + 1

var LeftMark = sink.Add("left.var")

var LeftReady bool

func init() {
	sink.Add("left.init")
	LeftReady = base.BaseReady && Left == 11 && LeftMark == "left.var"
}
