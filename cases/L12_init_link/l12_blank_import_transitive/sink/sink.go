// Package sink is the shared observation point for blank-import side effects.
package sink

import "sync"

type store struct {
	mu     sync.Mutex
	marks  []string
	counts map[string]int
	values map[string]int
}

var st = store{counts: make(map[string]int), values: make(map[string]int)}

// Add appends one mark and returns s unchanged.
func Add(s string) string {
	st.mu.Lock()
	st.marks = append(st.marks, s)
	st.mu.Unlock()
	return s
}

// Marks returns a stable copy of the marks.
func Marks() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]string, len(st.marks))
	copy(out, st.marks)
	return out
}

// Bump increments the named counter.
func Bump(key string) {
	st.mu.Lock()
	st.counts[key]++
	st.mu.Unlock()
}

// Count returns the named counter.
func Count(key string) int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.counts[key]
}

// Set records an integer value.
func Set(key string, v int) {
	st.mu.Lock()
	st.values[key] = v
	st.mu.Unlock()
}

// Get reads an integer value.
func Get(key string) (int, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	v, ok := st.values[key]
	return v, ok
}
