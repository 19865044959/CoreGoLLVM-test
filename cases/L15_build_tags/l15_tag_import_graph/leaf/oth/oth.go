package oth

import "l15_tag_import_graph/trace"

const Name = "oth"

const Scale int64 = 5

type Item struct {
	id   int64
	name string
	next *Item
}

func (i *Item) ID() int64 { return i.id }

func (i *Item) Label() string { return i.name }

func (i *Item) NextLabel() string {
	if i.next == nil {
		return ""
	}
	return i.next.name
}

var Node = &Item{id: 21, name: Name}

func init() {
	Node.next = &Item{id: 22, name: "oth-next"}
	trace.Mark("oth")
}
