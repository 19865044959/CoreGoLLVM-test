// Package mid depends on base and must be initialized after it.
package mid

import (
	"l12_module_init_order/base"
	"l12_module_init_order/sink"
)

var Mid = base.Base*10 + 2

var MidMark = sink.Add("mid.var")

var MidReady bool

func init() {
	sink.Add("mid.init")
	MidReady = base.BaseReady && Mid == 102 && MidMark == "mid.var"
}
