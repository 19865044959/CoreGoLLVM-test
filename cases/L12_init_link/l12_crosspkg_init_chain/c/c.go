// Package c is the bottom of the linear c -> b -> a chain.
package c

import "l12_crosspkg_init_chain/sink"

var (
	V     = 3
	Mark  = sink.Add("c.var")
	Ready bool
)

func init() {
	sink.Add("c.init")
	Ready = V == 3 && Mark == "c.var"
}
