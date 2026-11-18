// Package limits exports typed and untyped constants at the edges of their
// representable ranges. Importing packages use them in const expressions,
// array lengths and constant conversions.
package limits

// Width, Level and Flags are distinct typed constant families.
type Width uint8
type Level int8
type Flags uint16

const (
	ZeroLen = 0
	Slots   = 17
	Blocks  = 1 << 6
	Total   = Slots * Blocks
)

// Boundary values of typed constants. Flags(HighBit) is a constant conversion;
// the result stays typed Flags.
const (
	MaxWidth Width = 255
	MinLevel Level = -128
	HighBit        = 1 << 15
	FlagsAll Flags = Flags(HighBit) | 0x00ff
)

// String and rune-length constants exercise constant string concatenation and
// len() folding across packages.
const (
	Word       = "go" + "-" + "l12"
	WordLen    = len(Word)
	Suffix     = "/derived"
	Name       = Word + Suffix
	NameLen    = len(Name)
	Pair       = `a` + `b` + `c`
	Unicode    = "héllo"
	UnicodeLen = len(Unicode)
)

// Complex and untyped arithmetic constants keep untyped defaults until they
// are boxed.
const (
	CReal = 3
	CImag = 4
	CVal  = complex(CReal, CImag)
	CAbs2 = CReal*CReal + CImag*CImag
)

// Untyped constants at the int64 / uint64 boundaries.
const (
	Shifted    = uint64(1) << 63
	Huge       = 1 << 40
	MinI64     = -1 << 63
	MaxI64     = 1<<63 - 1
	ExactFloat = 1.0/4 + 1.0/8
)
