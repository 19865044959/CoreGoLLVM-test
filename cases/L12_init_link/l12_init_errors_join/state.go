package main

import (
	"errors"
)

// detail is a pointer payload attached to an init error; it must remain valid
// and identity-stable across explicit GC.
type detail struct {
	Code    int
	Tag     string
	Payload []byte
}

type taggedError struct {
	Op     string
	Detail *detail
	Err    error
}

func (e *taggedError) Error() string { return e.Op + ": " + e.Err.Error() }
func (e *taggedError) Unwrap() error { return e.Err }

var (
	slots     [3]error
	initMarks []string

	detailAlpha = &detail{Code: 5, Tag: "alpha", Payload: []byte("alpha-payload")}

	errBadValue      = errors.New("invalid value: -5")
	errDupKey        = errors.New("duplicate key: dup")
	errPanicSentinel = errors.New("panic sentinel")
	errEmptyKey      = errors.New("empty key")
	errNegative      = errors.New("negative value")
)

// aggregate joins non-nil slots in fixed slot order, so the error text is
// independent of the undefined cross-file init order.
func aggregate(list []error) error {
	var nonNil []error
	for _, e := range list {
		if e != nil {
			nonNil = append(nonNil, e)
		}
	}
	if len(nonNil) == 0 {
		return nil
	}
	return errors.Join(nonNil...)
}

func aggregateText(list []error) string {
	e := aggregate(list)
	if e == nil {
		return ""
	}
	return e.Error()
}

func init() {
	initMarks = append(initMarks, "main")
}
