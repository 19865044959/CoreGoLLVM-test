package main

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	errDuplicate = errors.New("duplicate registration")
	errFrozen    = errors.New("registry frozen")
)

type impl struct {
	tag  string
	data []byte
}

type regEntry struct {
	name  string
	value int
	impl  *impl
}

type registry struct {
	mu      sync.Mutex
	entries map[string]*regEntry
	frozen  bool
}

func newRegistry() *registry {
	return &registry{entries: make(map[string]*regEntry, 8)}
}

// Register 检测重名；冻结后拒绝任何注册。重复注册不覆盖旧值。
func (r *registry) Register(name string, value int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return fmt.Errorf("%w: %s", errFrozen, name)
	}
	if _, ok := r.entries[name]; ok {
		return fmt.Errorf("%w: %s", errDuplicate, name)
	}
	data := make([]byte, 16)
	for i := range data {
		data[i] = byte(value + i)
	}
	r.entries[name] = &regEntry{
		name:  name,
		value: value,
		impl:  &impl{tag: name + "#v1", data: data},
	}
	return nil
}

func (r *registry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *registry) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

func (r *registry) Lookup(name string) (*regEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	return e, ok
}

// Names 返回排序后的名字快照，保证输出顺序稳定。
func (r *registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.entries))
	for n := range r.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func (r *registry) Dump() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.entries))
	for n := range r.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%s=%d", n, r.entries[n].value)
	}
	return out
}
