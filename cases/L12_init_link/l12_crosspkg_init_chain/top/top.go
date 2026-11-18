// Package top imports the whole chain plus the diamond branches.
package top

import (
	"l12_crosspkg_init_chain/a"
	"l12_crosspkg_init_chain/dl"
	"l12_crosspkg_init_chain/dr"
	"l12_crosspkg_init_chain/leaf"
	"l12_crosspkg_init_chain/sink"
)

var (
	V     = a.V*10 + dl.V + dr.V + leaf.V
	Seen  = a.Ready && dl.Ready && dr.Ready && leaf.Ready
	Mark  = sink.Add("top.var")
	Ready bool
)

func init() {
	sink.Add("top.init")
	Ready = Seen && V == 250 && Mark == "top.var"
}
