package main

import (
	"errors"
	"sort"
	"strings"
)

var (
	errDuplicate = errors.New("duplicate registration")
	errFrozen    = errors.New("registry frozen")
	errEmptyName = errors.New("empty name")
)

type plugin struct {
	name   string
	factor int
	data   *[32]byte
}

type pluginRegistry struct {
	m      map[string]*plugin
	frozen bool
}

var plugins = newPluginRegistry()

func newPluginRegistry() *pluginRegistry {
	return &pluginRegistry{m: make(map[string]*plugin, 8)}
}

func canonical(name string) string { return strings.ToLower(name) }

func (r *pluginRegistry) add(name string, factor int) (*plugin, error) {
	if name == "" {
		return nil, errEmptyName
	}
	if r.frozen {
		return nil, errFrozen
	}
	key := canonical(name)
	if _, ok := r.m[key]; ok {
		return nil, errDuplicate
	}
	data := new([32]byte)
	for i := range data {
		data[i] = byte(factor + i)
	}
	p := &plugin{name: key, factor: factor, data: data}
	r.m[key] = p
	return p, nil
}

func (r *pluginRegistry) freeze() { r.frozen = true }

func (r *pluginRegistry) lookup(name string) *plugin { return r.m[canonical(name)] }

func (r *pluginRegistry) count() int { return len(r.m) }

func (r *pluginRegistry) names() []string {
	out := make([]string, 0, len(r.m))
	for name := range r.m {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

type routePanic struct {
	pattern string
	kind    string
}

func (p *routePanic) Error() string { return p.kind + ": " + p.pattern }

var errNilHandler = errors.New("nil handler")

type routeTable struct {
	m map[string]func(int) int
}

var routes = &routeTable{m: make(map[string]func(int) int, 8)}

func (rt *routeTable) handle(pattern string, fn func(int) int) {
	if fn == nil {
		panic(errNilHandler)
	}
	if _, ok := rt.m[pattern]; ok {
		panic(&routePanic{pattern: pattern, kind: "duplicate route"})
	}
	rt.m[pattern] = fn
}

func catchRoutePanic(fn func()) (p *routePanic, other error) {
	defer func() {
		if r := recover(); r != nil {
			if rp, ok := r.(*routePanic); ok {
				p = rp
				return
			}
			if err, ok := r.(error); ok {
				other = err
				return
			}
			other = errors.New("non-error panic")
		}
	}()
	fn()
	return nil, nil
}
