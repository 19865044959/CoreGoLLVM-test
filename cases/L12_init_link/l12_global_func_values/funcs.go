package main

import "fmt"

type counter struct {
	base int
	bump int
}

func (c *counter) Add(x int) int { return c.base + x }
func (c *counter) Mul(x int) int { return c.base * x }
func (c *counter) Bump() int {
	c.bump++
	return c.bump
}

type appState struct {
	applied int
	last    int
}

type handler func(int) int

func plus(n int) func(int) int {
	return func(x int) int { return x + n }
}

func double(x int) int { return x * 2 }

func compose(f, g func(int) int) func(int) int {
	return func(x int) int { return g(f(x)) }
}

var (
	shared = &counter{base: 7}
	state  = &appState{}

	// Method values, closures and plain function values all created during
	// package variable initialization.
	table = map[string]handler{
		"add":    shared.Add,
		"mul":    shared.Mul,
		"add10":  func(x int) int { return shared.Add(x) + 10 },
		"state":  func(x int) int { state.applied += x; state.last = x; return state.applied },
		"double": double,
	}
	exprs = map[string]func(*counter, int) int{
		"add": (*counter).Add,
		"mul": (*counter).Mul,
	}
	chain  = map[string]func(int) int{"plus3thenDouble": compose(plus(3), double)}
	bumpFn = shared.Bump

	// Package-level variable initializers may call values stored by earlier
	// initializers.
	sumProbe       = table["add"](5) + table["mul"](3) // 12 + 21
	methodProbe    = shared.Add(1)                     // 8
	initExprsProbe = exprs["add"](shared, 4)           // 11
	initFuncType   = fmt.Sprintf("%T", table["add"])   // main.handler

	initChecks int
)

func init() {
	initChecks = 1
}
