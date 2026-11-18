package main

import "sync/atomic"

type node struct {
	id      int
	score   int
	payload []byte
	next    *node
}

type graphT struct {
	head  *node
	nodes []*node
}

var buildCount int32

// buildGraph is called from a package-level variable initializer, i.e. before
// any init function runs.
func buildGraph(n int) *graphT {
	atomic.AddInt32(&buildCount, 1)
	nodes := make([]*node, n)
	var head, tail *node
	for i := 0; i < n; i++ {
		nd := &node{id: i, score: i*3 + 1, payload: make([]byte, 8)}
		for j := range nd.payload {
			nd.payload[j] = byte((i*5 + j) & 0xff)
		}
		nodes[i] = nd
		if head == nil {
			head = nd
		} else {
			tail.next = nd
		}
		tail = nd
	}
	return &graphT{head: head, nodes: nodes}
}

var graph = buildGraph(96)
