package report

import (
	"l15_module_var_deps/calc"
	"l15_module_var_deps/seed"
	"l15_module_var_deps/trace"
)

var Total = calc.Doubled + calc.Plus + seed.Base

var Items = []*seed.Item{seed.Node, {ID: 3, Label: "report"}}

func init() { trace.Mark("report") }
