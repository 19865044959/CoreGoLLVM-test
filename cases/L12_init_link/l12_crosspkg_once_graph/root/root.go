// Package root joins the two lazy branches; its Once body waits for both.
package root

import (
	"sync"
	"sync/atomic"

	"l12_crosspkg_once_graph/left"
	"l12_crosspkg_once_graph/right"
	"l12_crosspkg_once_graph/sink"
)

// Node is the immutable root of the lazy graph.
type Node struct {
	ID    int
	Label string
	Left  *left.Node
	Right *right.Node
}

var (
	buildCount int32
	once       sync.Once
	node       *Node
	InitBuilds int
)

// Ensure builds the root node at most once, after both branches are ready.
func Ensure() *Node {
	once.Do(func() {
		atomic.AddInt32(&buildCount, 1)
		l := left.Ensure()
		r := right.Ensure()
		node = &Node{ID: 100, Label: "root", Left: l, Right: r}
		sink.Add("root.build")
	})
	return node
}

// Builds reports how many times the Once body ran.
func Builds() int { return int(atomic.LoadInt32(&buildCount)) }

func init() {
	InitBuilds = Builds()
	sink.Add("root.init")
}
