// Package layerops contributes high-priority overrides, an equal-priority
// tie-break participant and tombstones. It is imported for side effects by
// main.
package layerops

import "l12_registry_overlay_merge/reg"

func init() {
	must(reg.Register(reg.Contribution{
		Key: "db", Priority: 9, Owner: "ops",
		Name: "ops-db",
		Meta: &reg.Meta{Tag: "ops-db", Payload: []byte("m-ops")},
	}))
	must(reg.Register(reg.Contribution{
		Key: "queue", Priority: 6, Owner: "aaa",
		Name: "aaa-name",
	}))
	must(reg.Register(reg.Contribution{
		Key: "legacy", Priority: 20, Owner: "ops", Tombstone: true,
	}))
	must(reg.Register(reg.Contribution{
		Key: "cache", Priority: 20, Owner: "ops", Tombstone: true,
	}))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
