package main

import "strconv"

const builtinCount = 64

type entry struct {
	id      int
	name    string
	score   int64
	payload *[64]byte
	next    *entry
}

type tableSet struct {
	byID     map[int]*entry
	ordered  []*entry
	groups   map[string][]*entry
	cross    map[*entry][]*entry
	payloads []map[string]*entry
	tail     *entry
}

var tables = buildTables(builtinCount)

func groupName(g int) string { return "g" + strconv.Itoa(g) }

func newPayload(key int) *[64]byte {
	p := new([64]byte)
	for i := 0; i < len(p); i++ {
		p[i] = byte(key*7 + i)
	}
	return p
}

func newEntry(id int) *entry {
	return &entry{
		id:      id,
		name:    "e" + strconv.Itoa(id),
		score:   int64(id)*3 + 1,
		payload: newPayload(id),
	}
}

func buildTables(n int) *tableSet {
	ts := &tableSet{
		byID:     make(map[int]*entry, n),
		ordered:  make([]*entry, 0, n),
		groups:   make(map[string][]*entry, 4),
		cross:    make(map[*entry][]*entry, n),
		payloads: make([]map[string]*entry, 0, n),
	}
	for i := 0; i < n; i++ {
		e := newEntry(i)
		ts.byID[e.id] = e
		ts.ordered = append(ts.ordered, e)
		g := groupName(e.id % 4)
		ts.groups[g] = append(ts.groups[g], e)
		ts.payloads = append(ts.payloads, map[string]*entry{e.name: e})
		if ts.tail != nil {
			ts.tail.next = e
		}
		ts.tail = e
	}
	for i, e := range ts.ordered {
		ts.cross[e] = []*entry{ts.ordered[(i+1)%n], ts.ordered[(i+7)%n]}
	}
	return ts
}

// insert appends one entry to every global table and links it into the list.
func (ts *tableSet) insert(e *entry) {
	ts.byID[e.id] = e
	ts.ordered = append(ts.ordered, e)
	g := groupName(e.id % 4)
	ts.groups[g] = append(ts.groups[g], e)
	ts.payloads = append(ts.payloads, map[string]*entry{e.name: e})
	ts.cross[e] = []*entry{ts.ordered[0], ts.ordered[1]}
	if ts.tail != nil {
		ts.tail.next = e
	}
	ts.tail = e
}

func keyName(i int) string { return "e" + strconv.Itoa(i) }
