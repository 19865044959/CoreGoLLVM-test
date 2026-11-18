package main

import "fmt"

func init() {
	alphaDetail = &detail{tag: "alpha-payload", payload: []byte("payload-a")}
	slotA = &taggedError{
		op:     "load",
		Detail: alphaDetail,
		Err:    fmt.Errorf("%w: entry %d: %w", errAlpha, 2, errBadValue),
	}
	initOrder = append(initOrder, "alpha")
}
