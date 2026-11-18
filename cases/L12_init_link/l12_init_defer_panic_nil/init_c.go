package main

import "fmt"

var (
	cRecovered     any
	cRecoveredType string
	cDirect        any
)

func recoverHelper() (r any) {
	defer func() {
		r = recover()
	}()
	panic("c-helper")
}

func init() {
	trace = append(trace, "c:enter")
	// recover outside a deferred function always returns nil.
	cDirect = recover()
	cRecovered = recoverHelper()
	cRecoveredType = fmt.Sprintf("%T", cRecovered)
	trace = append(trace, "c:after-helper")
	defer func() {
		trace = append(trace, "c:defer")
	}()
}
