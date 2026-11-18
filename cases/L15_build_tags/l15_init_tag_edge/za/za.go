package za

import "l15_init_tag_edge/trace"

type Item struct {
	id   int64
	name string
}

func (i *Item) ID() int64 { return i.id }

func (i *Item) Label() string { return i.name }

var V int64 = 11

var Node = &Item{id: 11, name: "za"}

func init() { trace.Mark("za") }
