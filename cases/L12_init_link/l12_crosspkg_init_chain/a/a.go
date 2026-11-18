// Package a imports b, completing the linear chain c -> b -> a.
package a

import (
	"l12_crosspkg_init_chain/b"
	"l12_crosspkg_init_chain/sink"
)

var (
	V     = b.V*3 + 1
	Mark  = sink.Add("a.var")
	Ready bool
)

func init() {
	sink.Add("a.init")
	Ready = b.Ready && V == 22 && Mark == "a.var"
}
