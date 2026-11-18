// Package left lazily builds a node that depends on base.Ensure.
package left

import (
	"sync"
	"sync/atomic"

	"l12_crosspkg_once_graph/base"
	"l12_crosspkg_once_graph/sink"
)

// Node is the immutable object built lazily by Ensure.
type Node struct {
	ID    int
	Label string
	Base  *base.Node
}

var (
	buildCount int32
	once       sync.Once
	node       *Node
	InitBuilds int
)

// Ensure builds the left node at most once, after base is ready.
func Ensure() *Node {
	once.Do(func() {
		atomic.AddInt32(&buildCount, 1)
		b := base.Ensure()
		node = &Node{ID: 21, Label: "left", Base: b}
		sink.Add("left.build")
	})
	return node
}

// Builds reports how many times the Once body ran.
func Builds() int { return int(atomic.LoadInt32(&buildCount)) }

func init() {
	InitBuilds = Builds()
	sink.Add("left.init")
}
