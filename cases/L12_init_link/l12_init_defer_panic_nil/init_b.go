package main

import "fmt"

var (
	bRecovered     any
	bRecoveredType string
)

func init() {
	trace = append(trace, "b:enter")
	// Registered first, so runs last: recovers the panic raised by the
	// deferred function below while the original panic is being unwound.
	defer func() {
		r := recover()
		bRecovered = r
		bRecoveredType = fmt.Sprintf("%T", r)
		trace = append(trace, "b:recover-second")
	}()
	// Registered second, so runs first and panics during unwinding.
	defer func() {
		trace = append(trace, "b:defer-panic")
		panic("b-second")
	}()
	trace = append(trace, "b:before")
	panic("b-first")
}
