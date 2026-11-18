package main

import "sort"

type handler struct {
	name   string
	prio   int
	seq    int
	weight int
}

type registry struct {
	m       map[string]*handler
	order   []*handler
	nextSeq int
}

var reg = newRegistry()

func newRegistry() *registry {
	return &registry{m: make(map[string]*handler, 16)}
}

// register appends a handler and assigns the next monotonic sequence number.
// Sequence numbers are never reused after unregister.
func register(name string, prio, weight int) *handler {
	h := &handler{name: name, prio: prio, seq: reg.nextSeq, weight: weight}
	reg.nextSeq++
	reg.m[name] = h
	reg.order = append(reg.order, h)
	return h
}

func unregister(name string) bool {
	h, ok := reg.m[name]
	if !ok {
		return false
	}
	delete(reg.m, name)
	for i, cur := range reg.order {
		if cur == h {
			reg.order[i] = nil
			break
		}
	}
	return true
}

func lookup(name string) *handler { return reg.m[name] }

// ordered returns live handlers in registration (sequence) order.
func ordered() []*handler {
	out := make([]*handler, 0, len(reg.m))
	for _, h := range reg.order {
		if h != nil {
			out = append(out, h)
		}
	}
	return out
}

// byPriority is a stable priority view: equal priorities keep sequence order.
func byPriority() []*handler {
	out := ordered()
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].prio < out[j].prio
	})
	return out
}
