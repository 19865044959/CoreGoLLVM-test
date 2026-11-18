// Package inner is the leaf subpackage of the module. Its init must finish
// before any importer's package-level variables are initialized.
package inner

import (
	"sync/atomic"

	"l12_subpkg_init_module/sink"
)

var (
	Value     = 7
	VarMark   = sink.Add("inner.var")
	Ready     bool
	initCount int32
)

func init() {
	atomic.AddInt32(&initCount, 1)
	sink.Add("inner.init")
	Ready = Value == 7 && VarMark == "inner.var"
}

// InitCount reports how many times this package's init ran.
func InitCount() int { return int(atomic.LoadInt32(&initCount)) }
