// Package top observes every dependency after they have finished initializing.
package top

import (
	"l12_module_init_order/base"
	"l12_module_init_order/left"
	"l12_module_init_order/mid"
	"l12_module_init_order/right"
	"l12_module_init_order/sink"
)

var Top = mid.Mid*10 + left.Left + right.Right + 3

// TopObserved must be true even though mid/left/right have no defined order
// relative to each other: all three are dependencies of top and therefore
// finish before top's initialization starts.
var TopObserved = base.BaseReady && mid.MidReady && left.LeftReady && right.RightReady

var TopMark = sink.Add("top.var")

var TopReady bool

func init() {
	sink.Add("top.init")
	TopReady = TopObserved && Top == 1046 && TopMark == "top.var"
}
