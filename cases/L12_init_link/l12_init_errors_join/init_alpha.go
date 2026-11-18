package main

import "fmt"

// init alpha records a wrapped configuration error instead of panicking.
func init() {
	initMarks = append(initMarks, "alpha")
	slots[0] = &taggedError{
		Op:     "alpha",
		Detail: detailAlpha,
		Err:    fmt.Errorf("bad entry %d: %w", 2, errBadValue),
	}
}
