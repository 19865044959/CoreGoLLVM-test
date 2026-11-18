// Package unit exports typed and untyped constants that importing packages use
// as array lengths, map keys and const expressions.
package unit

// Kind is a typed enum used in const expressions across package boundaries.
type Kind uint8

const (
	KindNone Kind = iota
	KindSmall
	KindLarge
	kindCount
)

const (
	Shift         = 4
	Scale         = 1 << Shift
	Mask          = Scale - 1
	MaxLen        = 3 * Scale
	Combined Kind = KindLarge<<4 | KindSmall

	// KindCount converts the unexported iota member to an exported int const.
	KindCount int = int(kindCount)
)

// Priority is a typed integer used in a second const chain.
type Priority int16

const (
	PriLow Priority = iota + 1
	PriHigh
)

// Weighted stays typed Priority because PriHigh is typed.
const Weighted = PriHigh * 10
