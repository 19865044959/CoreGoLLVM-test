// Package layerteam contributes mid-priority overrides and an equal-priority
// conflict. It is imported for side effects by main.
package layerteam

import "l12_registry_overlay_merge/reg"

func init() {
	must(reg.Register(reg.Contribution{
		Key: "db", Priority: 5, Owner: "team",
		Addr: "team:1", Weight: 5,
		Meta: &reg.Meta{Tag: "team-db", Payload: []byte("m-team")},
	}))
	must(reg.Register(reg.Contribution{
		Key: "queue", Priority: 6, Owner: "bbb",
		Name: "bbb-name", Addr: "bbb:6", Weight: 6,
	}))
	must(reg.Register(reg.Contribution{
		Key: "cache", Priority: 6, Owner: "team",
		Name: "cache-team", Weight: 7,
	}))
	must(reg.Register(reg.Contribution{
		Key: "onlyteam", Priority: 4, Owner: "team",
		Name: "only-team", Addr: "team:9", Weight: 9,
	}))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
