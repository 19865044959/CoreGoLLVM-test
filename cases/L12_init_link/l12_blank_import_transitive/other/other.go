// Package other imports inner normally, providing a second import path that
// must not re-run inner's init.
package other

import "l12_blank_import_transitive/inner"

var (
	Value = inner.Value + 1
	Ready bool
)

func init() {
	Ready = inner.Ready && Value == 6
}
