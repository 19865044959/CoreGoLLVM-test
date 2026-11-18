// Package other is a second importer of inner. It must not cause inner's init
// to run a second time.
package other

import "l12_subpkg_init_module/inner"

var (
	Value = inner.Value + 1
	Ready bool
	saw   int
)

func init() {
	saw = inner.InitCount()
	Ready = inner.Ready && Value == 8
}

// SeenInner reports inner's init counter as observed by this package.
func SeenInner() int { return saw }
