// Package sink records deterministic mark sequences for the module.
package sink

import "sync"

var (
	mu    sync.Mutex
	marks []string
)

// Add appends one mark.
func Add(s string) {
	mu.Lock()
	marks = append(marks, s)
	mu.Unlock()
}

// Marks returns a stable copy of all marks.
func Marks() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, len(marks))
	copy(out, marks)
	return out
}
