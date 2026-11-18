package b

import "l15_tag_cycle_break/trace"

var Name = "b"

func LocalB() string { return "local-b" }

func init() { trace.Mark("b") }
