package main

import "sync/atomic"

// registry is initialized before root because root's initializer passes it
// explicitly to registerInto.
var registry = map[int]*box{}

var e = &box{id: 42, v: d, tag: mkTag("e")}
var root = registerInto(registry, "root", e)

func registerInto(m map[int]*box, name string, b *box) *box {
	initLog = append(initLog, name)
	atomic.AddInt32(&regCount, 1)
	m[b.id] = b
	return b
}
