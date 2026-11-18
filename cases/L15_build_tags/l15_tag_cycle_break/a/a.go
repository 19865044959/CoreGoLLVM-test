package a

import "l15_tag_cycle_break/trace"

var A = "a"

func LocalA() string { return "local-a" }

func init() { trace.Mark("a") }
