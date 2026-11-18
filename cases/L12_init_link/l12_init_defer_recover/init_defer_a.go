package main

// init A registers three defers, so the block must end in strict LIFO order:
// defer3, defer2, defer1. The final defer also mutates a package-level pointer
// graph that main validates after explicit GC.
func init() {
	trace = append(trace, "a:enter")
	defer func() { trace = append(trace, "a:defer1") }()
	defer func() { trace = append(trace, "a:defer2") }()
	defer func() {
		trace = append(trace, "a:defer3")
		probeRoot.value = 30
	}()
	trace = append(trace, "a:body")
}
