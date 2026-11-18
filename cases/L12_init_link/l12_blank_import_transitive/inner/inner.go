// Package inner is reached only through blank imports from plugin (and a normal
// import from other). Its init must run exactly once.
package inner

import "l12_blank_import_transitive/sink"

var (
	Value = 5
	Ready bool
)

func init() {
	sink.Add("inner.init")
	sink.Bump("inner")
	Ready = Value == 5
}
