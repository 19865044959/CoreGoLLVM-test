// Package sink records package-initialization marks for the module.
package sink

import "sync"

var (
	mu    sync.Mutex
	marks []string
)

// Add appends one mark and returns s unchanged.
func Add(s string) string {
	mu.Lock()
	marks = append(marks, s)
	mu.Unlock()
	return s
}

// Marks returns a stable copy of the trace.
func Marks() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, len(marks))
	copy(out, marks)
	return out
}

// Index returns the position of m, or -1.
func Index(m string) int {
	mu.Lock()
	defer mu.Unlock()
	for i, v := range marks {
		if v == m {
			return i
		}
	}
	return -1
}
