//go:build linux

package hub

import (
	"l15_tag_import_graph/leaf/lnx"
	"l15_tag_import_graph/trace"
)

func platformInfo() (string, int64) { return lnx.Name, lnx.Scale }

func platformBias() int64 { return 7 }

func platformNodeAny() any { return lnx.Node }

func init() { trace.Mark("route-lnx") }
