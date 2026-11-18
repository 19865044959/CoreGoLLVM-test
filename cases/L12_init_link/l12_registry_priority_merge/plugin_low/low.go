// Package plugin_low registers the low-priority side of each conflicting key
// from its init. It is only imported for its side effects.
package plugin_low

import "l12_registry_priority_merge/reg"

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func init() {
	must(reg.Register(reg.Contribution{
		Key: "codec", Priority: 5, Owner: "beta",
		Data: []byte("low-codec"), Meta: &reg.Meta{Tag: "low", Score: 5},
	}))
	must(reg.Register(reg.Contribution{
		Key: "tie", Priority: 7, Owner: "aaa",
		Data: []byte("low-tie"), Meta: &reg.Meta{Tag: "low-tie", Score: 7},
	}))
	must(reg.Register(reg.Contribution{
		Key: "low-only", Priority: 9, Owner: "beta",
		Data: []byte("only-low"), Meta: &reg.Meta{Tag: "low-only", Score: 9},
	}))
}
