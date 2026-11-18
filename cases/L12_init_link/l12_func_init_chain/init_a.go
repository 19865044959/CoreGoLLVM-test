package main

// initSeen is set by an init function, which must run after every package-level
// variable initializer in this package.
var initSeen int

func init() {
	initLog = append(initLog, "init")
	initSeen = d + e.v
}
