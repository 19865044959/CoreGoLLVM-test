package mid

import (
	"l15_module_tag_var_chain/conf"
	"l15_module_tag_var_chain/trace"
)

var Scale = conf.Base + 1

var Table = []int64{conf.Base, conf.Base * 2, Scale}

var Items = []*conf.Item{conf.Node, {ID: Scale, Label: "mid"}}

func init() { trace.Mark("mid") }
