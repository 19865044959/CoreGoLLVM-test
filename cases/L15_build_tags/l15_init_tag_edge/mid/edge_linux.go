//go:build linux

package mid

import (
	"l15_init_tag_edge/trace"
	"l15_init_tag_edge/za"
)

var Edge = za.V

func EdgeName() string { return "za" }

func EdgeNodeAny() any { return za.Node }

func init() { trace.Mark("edge-za") }
