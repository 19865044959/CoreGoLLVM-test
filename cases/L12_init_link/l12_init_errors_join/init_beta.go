package main

import "fmt"

// init beta recovers a panic and converts it into the same error tree.
func init() {
	initMarks = append(initMarks, "beta")
	func() {
		defer func() {
			if r := recover(); r != nil {
				if s, ok := r.(string); ok && s == "init boom" {
					slots[1] = &taggedError{
						Op:  "beta",
						Err: fmt.Errorf("recovered panic: %w", errPanicSentinel),
					}
				}
			}
		}()
		panic("init boom")
	}()
}
