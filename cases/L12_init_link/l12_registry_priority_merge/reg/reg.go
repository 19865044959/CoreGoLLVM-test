// Package reg merges contributions from independent init functions with a
// deterministic, order-independent policy: higher priority wins; equal
// priority is broken by lexicographically smaller owner.
package reg

import (
	"errors"
	"sort"
	"sync"
)

// Meta is an immutable registration metadata object.
type Meta struct {
	Tag   string
	Score int
}

// Contribution is one registration attempt. Data is copied on Register so a
// caller mutating its slice cannot change the recorded entry.
type Contribution struct {
	Key      string
	Priority int
	Owner    string
	Data     []byte
	Meta     *Meta
}

// ErrFrozen is returned for registrations attempted after Freeze.
var ErrFrozen = errors.New("registry frozen")

var (
	mu     sync.Mutex
	frozen bool
	table  = map[string][]Contribution{}
)

// Register records one contribution or returns ErrFrozen.
func Register(c Contribution) error {
	mu.Lock()
	defer mu.Unlock()
	if frozen {
		return ErrFrozen
	}
	cp := c
	if c.Data != nil {
		cp.Data = append([]byte(nil), c.Data...)
	}
	table[c.Key] = append(table[c.Key], cp)
	return nil
}

// Freeze closes the registry for further writes. It is called from main's
// package variable initializer, after both plugin init functions completed.
func Freeze() {
	mu.Lock()
	frozen = true
	mu.Unlock()
}

// Frozen reports the freeze state.
func Frozen() bool {
	mu.Lock()
	defer mu.Unlock()
	return frozen
}

func winnerLocked(list []Contribution) Contribution {
	best := list[0]
	for _, c := range list[1:] {
		if c.Priority > best.Priority ||
			(c.Priority == best.Priority && c.Owner < best.Owner) {
			best = c
		}
	}
	return best
}

// Winner returns the effective contribution for a key.
func Winner(key string) (Contribution, bool) {
	mu.Lock()
	defer mu.Unlock()
	list := table[key]
	if len(list) == 0 {
		return Contribution{}, false
	}
	return winnerLocked(list), true
}

// History returns all contributions for a key ordered by owner, so the view is
// independent of the undefined cross-package init order.
func History(key string) []Contribution {
	mu.Lock()
	list := append([]Contribution(nil), table[key]...)
	mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].Owner != list[j].Owner {
			return list[i].Owner < list[j].Owner
		}
		return list[i].Priority < list[j].Priority
	})
	return list
}

// Keys returns all registered keys in sorted order.
func Keys() []string {
	mu.Lock()
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	mu.Unlock()
	sort.Strings(keys)
	return keys
}

// Total returns the number of recorded contributions.
func Total() int {
	mu.Lock()
	defer mu.Unlock()
	n := 0
	for _, list := range table {
		n += len(list)
	}
	return n
}
