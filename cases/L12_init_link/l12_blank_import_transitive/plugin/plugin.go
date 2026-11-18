// Package plugin is imported only for its side effects. It blank-imports inner,
// so inner's init runs first and its side effect is visible here.
package plugin

import (
	"l12_blank_import_transitive/sink"

	_ "l12_blank_import_transitive/inner"
)

var Value = 50

func init() {
	saw := sink.Count("inner")
	sink.Add("plugin.init")
	sink.Set("plugin.sawInner", saw)
	if saw == 1 && Value == 50 {
		sink.Set("plugin.ready", 1)
	}
}
