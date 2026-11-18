package main

import "fmt"

func init() {
	slotB = fmt.Errorf("beta: %w", fmt.Errorf("panic: %w", errPanicSentinel))
	initOrder = append(initOrder, "beta")
}
