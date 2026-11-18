//go:build linux

package main

import "unsafe"

type eventHeader struct {
	ID    uint64
	Flags uint64
	Seq   uint64
}

const platformLayout = "linux"

func newEvent(id, flags, seq uint64) *eventHeader {
	return &eventHeader{ID: id, Flags: flags, Seq: seq}
}

func inspectEvent(h *eventHeader) (uint64, uint64, uint64, uintptr, uintptr) {
	return h.ID, h.Flags, h.Seq, unsafe.Offsetof(h.Seq), unsafe.Sizeof(*h)
}
