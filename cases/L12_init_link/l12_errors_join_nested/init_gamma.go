package main

import (
	"errors"
	"fmt"
)

func init() {
	slotC = errors.Join(
		fmt.Errorf("gamma: %w", errGamma),
		&codeError{code: 7, Detail: &detail{tag: "code-payload", payload: []byte("payload-c")}},
	)
	slotD = errors.Join(
		errors.Join(errBadValue, errGamma),
		fmt.Errorf("delta: %w", errAlpha),
	)
	slotE = nil
	initOrder = append(initOrder, "gamma", "delta", "epsilon")
}
