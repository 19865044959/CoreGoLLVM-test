// Package b imports c, so c must be fully initialized first.
package b

import (
	"l12_crosspkg_init_chain/c"
	"l12_crosspkg_init_chain/sink"
)

var (
	V     = c.V*2 + 1
	Mark  = sink.Add("b.var")
	Ready bool
)

func init() {
	sink.Add("b.init")
	Ready = c.Ready && V == 7 && Mark == "b.var"
}
