// Package-level variables initialized through memoizing function calls.
//
// Explicit references in the initializer expressions (base, cfgA, cfgB) create
// the defined initialization dependencies; the memo maps make two initializers
// that request the same key share one object.
package main

import (
	"strconv"
	"sync/atomic"
)

type baseCfg struct {
	seed int
}

type cfg struct {
	key   string
	value int
	base  *baseCfg
}

var (
	built       = make(map[string]*cfg)
	allocCounts = make(map[string]int)

	buildCalls int32

	base = newBase()

	cfgA  = buildCfg("alpha", base)
	cfgA2 = buildCfg("alpha", base)
	cfgB  = buildCfg("beta", base)

	alias = cfgA
	adder = makeAdder(cfgB)
	view  = cfgA.view()

	boundView = cfgA.view

	initCalls  int32
	initAllocs int
)

func newBase() *baseCfg { return &baseCfg{seed: 11} }

// buildCfg memoizes by key: repeated calls with the same key return the same
// pointer, but every call is counted in buildCalls.
func buildCfg(key string, b *baseCfg) *cfg {
	atomic.AddInt32(&buildCalls, 1)
	if c, ok := built[key]; ok {
		return c
	}
	c := &cfg{key: key, value: len(key)*100 + b.seed, base: b}
	built[key] = c
	allocCounts[key]++
	return c
}

func makeAdder(c *cfg) func(int) int {
	return func(x int) int { return x + c.value }
}

func (c *cfg) view() string {
	return c.key + "=" + strconv.Itoa(c.value)
}

func init() {
	initCalls = atomic.LoadInt32(&buildCalls)
	initAllocs = len(allocCounts)
}
