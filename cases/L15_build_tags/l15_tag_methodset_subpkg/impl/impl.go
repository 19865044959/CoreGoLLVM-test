package impl

import "l15_tag_methodset_subpkg/iface"

type Node struct {
	ID   int64
	Name string
}

type Record struct {
	N int64
	P *Node
}

var _ iface.Value = (*Record)(nil)

func New(n int64) *Record {
	return &Record{N: n, P: &Node{ID: n, Name: "record"}}
}

func (r *Record) Get() int64 { return r.N }
