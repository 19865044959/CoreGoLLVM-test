// Package plugin1 registers the shared name "dup" during init.
package plugin1

import "l12_dup_register_crosspkg/reg"

const Value = 101

var outcome error

func init() {
	outcome = reg.Default.Register("dup", Value, "plugin1", &reg.Payload{Owner: "plugin1", Tag: "p1"})
}

// Outcome is the result of the init-time registration.
func Outcome() error { return outcome }
