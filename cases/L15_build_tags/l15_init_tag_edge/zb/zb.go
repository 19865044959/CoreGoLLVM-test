package zb

import "l15_init_tag_edge/trace"

type Item struct {
	id   int64
	name string
}

func (i *Item) ID() int64 { return i.id }

func (i *Item) Label() string { return i.name }

var V int64 = 13

var Node = &Item{id: 13, name: "zb"}

func init() { trace.Mark("zb") }
