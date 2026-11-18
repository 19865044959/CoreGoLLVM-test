package base

import "l15_init_tag_edge/trace"

type Item struct {
	id   int64
	name string
}

func (i *Item) ID() int64 { return i.id }

func (i *Item) Label() string { return i.name }

var V int64 = 5

var Node = &Item{id: 5, name: "base"}

func init() { trace.Mark("base") }
