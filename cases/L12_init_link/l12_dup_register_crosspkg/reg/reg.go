// Package reg is a process-wide registry shared by independently initialized
// plugin packages. Registration is order-independent: exactly one writer wins.
package reg

import (
	"errors"
	"sort"
	"sync"
)

// ErrDuplicate is returned when a name is already registered.
var ErrDuplicate = errors.New("duplicate registration")

// Payload is kept behind a pointer so GC must trace it.
type Payload struct {
	Owner string
	Tag   string
}

// Entry is one registered plugin.
type Entry struct {
	Name  string
	Value int
	Owner string
	Data  *Payload
}

// Registry is a concurrency-safe name -> entry table.
type Registry struct {
	mu sync.Mutex
	m  map[string]Entry
}

// New returns an empty registry.
func New() *Registry { return &Registry{m: make(map[string]Entry)} }

// Default is the process-wide registry used by all packages here.
var Default = New()

// Register inserts e under name unless the name already exists.
func (r *Registry) Register(name string, value int, owner string, data *Payload) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.m[name]; ok {
		return ErrDuplicate
	}
	r.m[name] = Entry{Name: name, Value: value, Owner: owner, Data: data}
	return nil
}

// Get returns a copy of the entry for name.
func (r *Registry) Get(name string) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.m[name]
	return e, ok
}

// Len reports the number of entries.
func (r *Registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m)
}

// Names returns the sorted entry names.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
