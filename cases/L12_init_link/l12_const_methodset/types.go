package main

import (
	"fmt"
	"strconv"
	"strings"
)

type level int

const (
	levelDebug level = iota
	levelInfo
	levelWarn
	levelError
)

var levelNames = [...]string{"debug", "info", "warn", "error"}

func (l level) String() string {
	if l >= 0 && int(l) < len(levelNames) {
		return levelNames[l]
	}
	return "level(" + strconv.Itoa(int(l)) + ")"
}

func (l level) Weight() int { return int(l) * 10 }

// Promote has a pointer receiver: it is in the method set of *level only.
func (l *level) Promote() level {
	*l = *l + 1
	return *l
}

type label string

const (
	labelAPI label = "api"
	labelDB  label = "db"
)

func (l label) String() string { return string(l) }
func (l label) Upper() string  { return strings.ToUpper(string(l)) }
func (l label) Kind() string   { return "label" }

type flags uint8

const (
	flagRead flags = 1 << iota
	flagWrite
	flagExec
)

const flagAll = flagRead | flagWrite | flagExec

func (f flags) String() string { return "flags(" + strconv.Itoa(int(f)) + ")" }

func (f flags) Has(other flags) bool { return f&other == other }

func (f flags) Count() int {
	c := 0
	for bit := flags(1); bit != 0; bit <<= 1 {
		if f&bit != 0 {
			c++
		}
	}
	return c
}

type stringer interface{ String() string }

type weighter interface {
	stringer
	Weight() int
}

type promoter interface{ Promote() level }

type upper interface {
	Upper() string
	Kind() string
}

// Compile-time method-set assertions. Constants are assignable to interfaces
// when the method set of their type (not *type) contains the methods.
var (
	_ stringer     = levelDebug
	_ weighter     = levelInfo
	_ upper        = labelAPI
	_ fmt.Stringer = levelWarn
	_ fmt.Stringer = labelDB
	_ fmt.Stringer = flags(0)
	_ promoter     = (*level)(nil)
)

var globalStringers = []fmt.Stringer{levelDebug, levelInfo, levelWarn, levelError, labelAPI, labelDB, flagRead}
var globalWeighter weighter = levelInfo
