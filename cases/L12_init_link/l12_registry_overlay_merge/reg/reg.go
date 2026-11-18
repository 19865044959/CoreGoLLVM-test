// Package reg implements a deterministic, field-level overlay registry.
// Contributions from independently initialized packages are merged by
// priority, then by owner name for equal priorities; registration order is
// never observable. Higher-priority non-empty fields overwrite lower-priority
// fields, and a tombstone clears everything accumulated below it.
package reg

import (
	"errors"
	"sort"
	"sync"
)

// Meta is an opaque pointer payload attached to a contribution.
type Meta struct {
	Tag     string
	Payload []byte
}

// Contribution is one layer's registration for a key.
type Contribution struct {
	Key       string
	Priority  int
	Owner     string
	Name      string
	Addr      string
	Weight    int
	Tombstone bool
	Meta      *Meta
}

// Merged is the deterministic overlay result for one key.
type Merged struct {
	Key     string
	Name    string
	Addr    string
	Weight  int
	Sources []string
	Meta    *Meta
}

// ErrFrozen is returned when a contribution arrives after Freeze.
var ErrFrozen = errors.New("registry frozen")

var (
	mu       sync.RWMutex
	frozen   bool
	contribs []Contribution
	// Snapshot is rebuilt on every call; no map iteration order escapes.
)

// Register records a contribution. It is expected to run during package init.
func Register(c Contribution) error {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		return ErrFrozen
	}
	contribs = append(contribs, c)
	return nil
}

// Freeze makes the registry immutable.
func Freeze() {
	mu.Lock()
	frozen = true
	mu.Unlock()
}

// Frozen reports whether the registry is immutable.
func Frozen() bool {
	mu.RLock()
	defer mu.RUnlock()
	return frozen
}

// Total returns the number of recorded contributions, including tombstones.
func Total() int {
	mu.RLock()
	defer mu.RUnlock()
	return len(contribs)
}

func less(a, b Contribution) bool {
	if a.Priority != b.Priority {
		return a.Priority < b.Priority
	}
	// For equal priority, larger owner names are applied first so the
	// lexicographically smallest owner's non-empty fields win.
	if a.Owner != b.Owner {
		return a.Owner > b.Owner
	}
	// Disambiguate exact duplicate owners deterministically.
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.Addr != b.Addr {
		return a.Addr < b.Addr
	}
	return a.Weight < b.Weight
}

func mergeOne(key string, cs []Contribution) (Merged, bool) {
	sorted := append([]Contribution(nil), cs...)
	sort.Slice(sorted, func(i, j int) bool { return less(sorted[i], sorted[j]) })

	m := Merged{Key: key}
	present := false
	for _, c := range sorted {
		if c.Tombstone {
			m = Merged{Key: key}
			present = false
			continue
		}
		present = true
		if c.Name != "" {
			m.Name = c.Name
		}
		if c.Addr != "" {
			m.Addr = c.Addr
		}
		if c.Weight != 0 {
			m.Weight = c.Weight
		}
		if c.Meta != nil {
			m.Meta = c.Meta
		}
		m.Sources = append(m.Sources, c.Owner)
	}
	return m, present
}

// Snapshot returns all surviving keys sorted by key name.
func Snapshot() []Merged {
	mu.RLock()
	cs := append([]Contribution(nil), contribs...)
	mu.RUnlock()

	byKey := make(map[string][]Contribution)
	for _, c := range cs {
		byKey[c.Key] = append(byKey[c.Key], c)
	}
	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]Merged, 0, len(keys))
	for _, k := range keys {
		if m, ok := mergeOne(k, byKey[k]); ok {
			out = append(out, m)
		}
	}
	return out
}

// Lookup returns the merged view for one key.
func Lookup(key string) (Merged, bool) {
	mu.RLock()
	var cs []Contribution
	for _, c := range contribs {
		if c.Key == key {
			cs = append(cs, c)
		}
	}
	mu.RUnlock()
	if len(cs) == 0 {
		return Merged{}, false
	}
	return mergeOne(key, cs)
}

// History returns the contributions for a key in deterministic application
// order (low priority first; equal priority: larger owner first).
func History(key string) []Contribution {
	mu.RLock()
	var cs []Contribution
	for _, c := range contribs {
		if c.Key == key {
			cs = append(cs, c)
		}
	}
	mu.RUnlock()
	sort.Slice(cs, func(i, j int) bool { return less(cs[i], cs[j]) })
	return cs
}
