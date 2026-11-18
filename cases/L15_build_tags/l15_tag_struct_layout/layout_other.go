//go:build !linux

package main

import "unsafe"

type eventHeader struct {
	ID    uint64
	Flags uint64
}

const platformLayout = "other"

func newEvent(id, flags, seq uint64) *eventHeader {
	return &eventHeader{ID: id, Flags: flags}
}

func inspectEvent(h *eventHeader) (uint64, uint64, uint64, uintptr, uintptr) {
	return h.ID, h.Flags, 0, 0, unsafe.Sizeof(*h)
}
