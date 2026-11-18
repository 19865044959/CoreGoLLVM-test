// Package sink records package-initialization marks for the whole module.
package sink

import "sync"

var (
	mu    sync.Mutex
	marks []string
)

// Add appends one init mark and returns s unchanged.
func Add(s string) string {
	mu.Lock()
	marks = append(marks, s)
	mu.Unlock()
	return s
}

// Marks returns a stable copy of the marks accumulated so far.
func Marks() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, len(marks))
	copy(out, marks)
	return out
}
