package base

import "l15_pkg_init_order/trace"

var Value int64 = compute()

func compute() int64 { return 21 }

func init() { trace.Mark("base") }
