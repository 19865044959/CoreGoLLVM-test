// Package plugin2 also registers the shared name "dup" during init. The two
// plugins are independent importers, so whichever runs first is unspecified;
// exactly one registration must win and the other must get ErrDuplicate.
package plugin2

import "l12_dup_register_crosspkg/reg"

const Value = 202

var outcome error

func init() {
	outcome = reg.Default.Register("dup", Value, "plugin2", &reg.Payload{Owner: "plugin2", Tag: "p2"})
}

// Outcome is the result of the init-time registration.
func Outcome() error { return outcome }
