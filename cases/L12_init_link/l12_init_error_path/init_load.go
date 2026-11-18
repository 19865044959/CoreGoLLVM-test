package main

import "fmt"

type fixtureEntry struct {
	name  string
	value int
}

var fixture = []fixtureEntry{
	{"alpha", 1},
	{"beta", -5},
	{"gamma", 3},
}

var (
	modInitErrs = map[string]error{}
	initSeq     []string
)

func init() {
	initSeq = append(initSeq, "load")
	if err := loadFixture(globalMods, fixture); err != nil {
		modInitErrs["load"] = err
	}
}

// loadFixture 遇到坏条目立即返回错误，已写入的条目保留。
func loadFixture(t *modTable, entries []fixtureEntry) error {
	for i, e := range entries {
		if err := t.put(e.name, e.value); err != nil {
			return fmt.Errorf("entry %d %q: %w", i+1, e.name, err)
		}
	}
	return nil
}
