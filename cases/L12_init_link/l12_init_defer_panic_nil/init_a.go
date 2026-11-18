package main

import "fmt"

var (
	nilRecovered     any
	nilRecoveredType string
)

func init() {
	trace = append(trace, "a:enter")
	defer func() {
		trace = append(trace, "a:defer")
	}()
	defer func() {
		r := recover()
		nilRecovered = r
		nilRecoveredType = fmt.Sprintf("%T", r)
		trace = append(trace, "a:recover")
	}()
	trace = append(trace, "a:before")
	panic(nil)
}
