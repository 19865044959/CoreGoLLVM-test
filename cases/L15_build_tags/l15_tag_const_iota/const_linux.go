//go:build linux

package main

type Level int

const (
	LvLow Level = iota + 1
	LvMid
	LvHigh
)

const (
	flavorName = "linux"
	flavorBase = 10
)

const derivedMask = flavorBase*2 + int(LvMid)

const derivedNext = int(LvHigh) + 1
