//go:build !linux

package mid

import (
	"l15_init_tag_edge/trace"
	"l15_init_tag_edge/zb"
)

var Edge = zb.V

func EdgeName() string { return "zb" }

func EdgeNodeAny() any { return zb.Node }

func init() { trace.Mark("edge-zb") }
