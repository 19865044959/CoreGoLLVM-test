// Package layerbase contributes the lowest-priority defaults. It is imported
// for side effects by main.
package layerbase

import "l12_registry_overlay_merge/reg"

func init() {
	must(reg.Register(reg.Contribution{
		Key: "db", Priority: 1, Owner: "base",
		Name: "base-db", Addr: "base:1", Weight: 1,
		Meta: &reg.Meta{Tag: "base-db", Payload: []byte("m-base")},
	}))
	must(reg.Register(reg.Contribution{
		Key: "cache", Priority: 1, Owner: "base",
		Name: "cache-base", Addr: "base:2", Weight: 2,
	}))
	must(reg.Register(reg.Contribution{
		Key: "queue", Priority: 1, Owner: "base",
		Name: "queue-base", Addr: "base:3", Weight: 3,
	}))
	must(reg.Register(reg.Contribution{
		Key: "legacy", Priority: 1, Owner: "base",
		Name: "legacy", Addr: "base:4", Weight: 4,
	}))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
