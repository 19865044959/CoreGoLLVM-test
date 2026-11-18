package lnx

import "l15_tag_import_graph/trace"

const Name = "lnx"

const Scale int64 = 2

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

var Node = &Item{id: 11, name: Name}

func init() {
	Node.next = &Item{id: 12, name: "lnx-next"}
	trace.Mark("lnx")
}
