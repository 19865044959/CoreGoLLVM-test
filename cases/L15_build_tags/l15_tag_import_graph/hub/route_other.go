//go:build !linux

package hub

import (
	"l15_tag_import_graph/leaf/oth"
	"l15_tag_import_graph/trace"
)

func platformInfo() (string, int64) { return oth.Name, oth.Scale }

func platformBias() int64 { return 9 }

func platformNodeAny() any { return oth.Node }

func init() { trace.Mark("route-oth") }
