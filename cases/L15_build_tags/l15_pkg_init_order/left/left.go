package left

import (
	"l15_pkg_init_order/base"
	"l15_pkg_init_order/trace"
)

var Derived = base.Value * 2

func init() { trace.Mark("left") }
