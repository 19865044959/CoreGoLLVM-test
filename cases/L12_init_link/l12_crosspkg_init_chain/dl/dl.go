// Package dl is one branch of the diamond; relative order with dr is undefined.
package dl

import (
	"l12_crosspkg_init_chain/leaf"
	"l12_crosspkg_init_chain/sink"
)

var (
	V     = leaf.V * 2
	Mark  = sink.Add("dl.var")
	Ready bool
)

func init() {
	sink.Add("dl.init")
	Ready = leaf.Ready && V == 10 && Mark == "dl.var"
}
