package main

// probe is a package-level pointer whose field is mutated from a deferred
// function running at the end of an init function.
type probe struct {
	value int
}

var (
	trace      []string
	probeRoot  = &probe{value: 1}
	bRecovered int
	cRecovered int
	cCompleted bool
)
