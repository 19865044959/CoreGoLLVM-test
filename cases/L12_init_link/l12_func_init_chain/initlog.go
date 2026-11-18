package main

import "sync/atomic"

// initLog records side effects performed by package-level initializers.
var initLog []string

var (
	mkCount  int32
	tagCount int32
	regCount int32
)

// mkInt is called from package-level variable initializers.
func mkInt(name string, v int) int {
	initLog = append(initLog, name)
	atomic.AddInt32(&mkCount, 1)
	return v
}

func add(a, b int) int { return a + b }

func mkTag(name string) string {
	initLog = append(initLog, name)
	atomic.AddInt32(&tagCount, 1)
	return name + "-tag"
}

type box struct {
	id  int
	v   int
	tag string
}

func (b *box) payloadOK() bool {
	return b != nil && b.id == 42 && b.v == 15 && b.tag == "e-tag"
}
