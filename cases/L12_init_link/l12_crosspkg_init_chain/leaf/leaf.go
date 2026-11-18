// Package leaf is shared by the diamond branches dl and dr.
package leaf

import "l12_crosspkg_init_chain/sink"

var (
	V     = 5
	Mark  = sink.Add("leaf.var")
	Ready bool
)

func init() {
	sink.Add("leaf.init")
	Ready = V == 5 && Mark == "leaf.var"
}
