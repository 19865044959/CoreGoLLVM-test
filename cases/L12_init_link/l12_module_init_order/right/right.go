// Package right is the other independent branch below top.
package right

import (
	"l12_module_init_order/base"
	"l12_module_init_order/sink"
)

var Right = base.Base + 2

var RightMark = sink.Add("right.var")

var RightReady bool

func init() {
	sink.Add("right.init")
	RightReady = base.BaseReady && Right == 12 && RightMark == "right.var"
}
