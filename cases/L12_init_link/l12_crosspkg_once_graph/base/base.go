// Package base is the leaf of the cross-package sync.Once graph.
package base

import (
	"sync"
	"sync/atomic"

	"l12_crosspkg_once_graph/sink"
)

// Node is the immutable object built lazily by Ensure.
type Node struct {
	ID    int
	Label string
}

var (
	buildCount int32
	once       sync.Once
	node       *Node

	// InitBuilds is captured by init and must be zero: lazy builders are not
	// allowed to run as an init side effect.
	InitBuilds int
)

// Ensure builds the base node at most once and returns the shared pointer.
func Ensure() *Node {
	once.Do(func() {
		atomic.AddInt32(&buildCount, 1)
		node = &Node{ID: 11, Label: "base"}
		sink.Add("base.build")
	})
	return node
}

// Builds reports how many times the Once body ran.
func Builds() int { return int(atomic.LoadInt32(&buildCount)) }

func init() {
	InitBuilds = Builds()
	sink.Add("base.init")
}
