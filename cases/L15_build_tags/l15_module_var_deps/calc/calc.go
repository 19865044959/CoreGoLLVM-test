package calc

import (
	"l15_module_var_deps/seed"
	"l15_module_var_deps/trace"
)

var Doubled = seed.Base * 2

var Plus = seed.Base + 1

var Table = buildTable()

func buildTable() []int64 {
	return []int64{seed.Base, seed.Base * 2, seed.Base + 1}
}

func init() { trace.Mark("calc") }
