//go:build !linux

package main

type Level int

const (
	LvLow Level = iota + 4
	LvMid
	LvHigh
)

const (
	flavorName = "other"
	flavorBase = 20
)

const derivedMask = flavorBase*2 + int(LvMid)

const derivedNext = int(LvHigh) + 1
