package main

import (
	"errors"
	"fmt"
)

var (
	errBadValue     = errors.New("invalid value")
	errEmptyKey     = errors.New("empty name")
	errDuplicateKey = errors.New("duplicate name")
)

type modEntry struct {
	name  string
	value int
}

type modTable struct {
	entries map[string]*modEntry
	order   []string
}

func newModTable() *modTable {
	return &modTable{entries: make(map[string]*modEntry, 8)}
}

// put 校验 name/value；失败不修改表，返回值可被 errors.Is 分类。
func (t *modTable) put(name string, value int) error {
	if name == "" {
		return errEmptyKey
	}
	if value < 0 {
		return fmt.Errorf("%w: %d", errBadValue, value)
	}
	if _, ok := t.entries[name]; ok {
		return fmt.Errorf("%w: %s", errDuplicateKey, name)
	}
	t.entries[name] = &modEntry{name: name, value: value}
	t.order = append(t.order, name)
	return nil
}

func (t *modTable) get(name string) (int, bool) {
	e, ok := t.entries[name]
	if !ok {
		return 0, false
	}
	return e.value, true
}

func (t *modTable) len() int { return len(t.entries) }

func (t *modTable) snapshot() map[string]int {
	out := make(map[string]int, len(t.entries))
	for k, e := range t.entries {
		out[k] = e.value
	}
	return out
}

// 包级表由 init 阶段填充；失败时保留已成功的条目。
var globalMods = newModTable()
