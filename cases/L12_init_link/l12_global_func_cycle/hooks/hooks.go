// Package hooks wires package-level function variables into a mutually
// recursive graph inside init. The function values are late-bound: even/odd
// refer to each other through package variables, fib refers to itself, and
// method values are bound to a global pointer receiver before main's package
// variable initializers run.
package hooks

import "sync/atomic"

type counter struct {
	base  int
	calls int32
}

var ctr = &counter{base: 3}

var (
	even func(int) bool
	odd  func(int) bool
	fib  func(int) int

	methodAdd func(int) int
	methodMul func(int) int

	chain []func(int) int
	table map[string]func(int) int
	iface any
)

func inc(n int) int    { return n + 1 }
func double(n int) int { return n * 2 }

func (c *counter) add(n int) int {
	atomic.AddInt32(&c.calls, 1)
	return c.base + n
}

func (c *counter) mul(n int) int {
	atomic.AddInt32(&c.calls, 1)
	return c.base * n
}

func init() {
	even = func(n int) bool {
		if n == 0 {
			return true
		}
		return odd(n - 1)
	}
	odd = func(n int) bool {
		if n == 0 {
			return false
		}
		return even(n - 1)
	}
	fib = func(n int) int {
		if n < 2 {
			return n
		}
		return fib(n-1) + fib(n-2)
	}

	methodAdd = ctr.add
	methodMul = ctr.mul

	chain = []func(int) int{inc, double, fib}
	table = map[string]func(int) int{
		"inc":    inc,
		"double": double,
		"fib":    fib,
		"method": methodAdd,
		"hop":    func(n int) int { return n + 10 },
	}
	iface = inc
}

// Even and Odd expose the recursive global function-value cycle.
func Even(n int) bool { return even(n) }
func Odd(n int) bool  { return odd(n) }

// Fib exposes the self-referential global function value.
func Fib(n int) int { return fib(n) }

// Increment is an ordinary exported function value target.
func Increment(n int) int { return inc(n) }

// Lookup returns a function value from the package-level dispatch table.
func Lookup(key string) func(int) int { return table[key] }

// Chain returns a copy of the pipeline function slice.
func Chain() []func(int) int { return append([]func(int) int(nil), chain...) }

// Iff returns the function value stored in a package-level interface.
func Iff() any { return iface }

// SetBase changes the pointer receiver seen by the bound method values.
func SetBase(v int) { ctr.base = v }

// Calls reports how often the bound method values ran.
func Calls() int32 { return atomic.LoadInt32(&ctr.calls) }

// ExportsMeta reports the static shape of the wired graph.
func ExportsMeta() (chainLen, tableLen int) { return len(chain), len(table) }
