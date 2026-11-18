package main

import (
	"errors"
	"fmt"
)

var (
	errAlpha         = errors.New("alpha")
	errBadValue      = errors.New("bad value")
	errPanicSentinel = errors.New("panic sentinel")
	errGamma         = errors.New("gamma")
	errCode          = errors.New("code match")
	errUnrelated     = errors.New("unrelated")
)

type detail struct {
	tag     string
	payload []byte
}

type taggedError struct {
	op     string
	Detail *detail
	Err    error
}

func (e *taggedError) Error() string { return e.op + ": " + e.Err.Error() }
func (e *taggedError) Unwrap() error { return e.Err }

// codeError implements a custom Is so errors.Is can match a sentinel by code
// even though the underlying error text is unrelated.
type codeError struct {
	code   int
	Detail *detail
}

func (e *codeError) Error() string { return fmt.Sprintf("code %d", e.code) }
func (e *codeError) Is(target error) bool {
	return target == errCode && e.code == 7
}

var (
	alphaDetail *detail
	slotA       error
	slotB       error
	slotC       error
	slotD       error
	slotE       error
	initOrder   []string
)
