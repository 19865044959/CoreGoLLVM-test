// Package plugin_high registers the high-priority side of each conflicting
// key from its init. It is only imported for its side effects.
package plugin_high

import "l12_registry_priority_merge/reg"

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func init() {
	must(reg.Register(reg.Contribution{
		Key: "codec", Priority: 10, Owner: "alpha",
		Data: []byte("high-codec"), Meta: &reg.Meta{Tag: "high", Score: 10},
	}))
	must(reg.Register(reg.Contribution{
		Key: "tie", Priority: 7, Owner: "zzz",
		Data: []byte("high-tie"), Meta: &reg.Meta{Tag: "high-tie", Score: 7},
	}))
	must(reg.Register(reg.Contribution{
		Key: "high-only", Priority: 3, Owner: "alpha",
		Data: []byte("only-high"), Meta: &reg.Meta{Tag: "high-only", Score: 3},
	}))
}
