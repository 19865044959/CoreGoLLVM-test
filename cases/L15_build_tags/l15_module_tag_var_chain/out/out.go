package out

import (
	"l15_module_tag_var_chain/conf"
	"l15_module_tag_var_chain/mid"
	"l15_module_tag_var_chain/trace"
)

var Total = mid.Scale*2 + conf.Base

var Items = []*conf.Item{mid.Items[0], mid.Items[1], {ID: Total, Label: "out"}}

var Start = int(conf.PlatformBias) % len(Items)

func init() { trace.Mark("out") }
