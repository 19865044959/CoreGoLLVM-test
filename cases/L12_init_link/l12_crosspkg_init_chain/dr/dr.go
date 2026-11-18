// Package dr is the other diamond branch; relative order with dl is undefined.
package dr

import (
	"l12_crosspkg_init_chain/leaf"
	"l12_crosspkg_init_chain/sink"
)

var (
	V     = leaf.V * 3
	Mark  = sink.Add("dr.var")
	Ready bool
)

func init() {
	sink.Add("dr.init")
	Ready = leaf.Ready && V == 15 && Mark == "dr.var"
}
