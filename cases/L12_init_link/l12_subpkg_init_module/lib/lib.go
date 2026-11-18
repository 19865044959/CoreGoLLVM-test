// Package lib is a subpackage that depends on inner and observes that inner
// has already finished initializing.
package lib

import (
	"l12_subpkg_init_module/inner"
	"l12_subpkg_init_module/sink"
)

var (
	Value    = inner.Value*10 + 1
	VarMark  = sink.Add("lib.var")
	Ready    bool
	snapshot []string
)

func init() {
	sink.Add("lib.init")
	Ready = inner.Ready && Value == 71 && VarMark == "lib.var"
	// Snapshot is taken after lib.init ran, so it contains exactly the four
	// marks of the linear inner -> lib dependency chain.
	snapshot = sink.Marks()
}

// Snapshot returns the init marks visible when lib.init finished.
func Snapshot() []string {
	out := make([]string, len(snapshot))
	copy(out, snapshot)
	return out
}

// InnerReady exposes the dependency state observed by lib.
func InnerReady() bool { return inner.Ready }
