// Compile-time constant initialization: chains of constant expressions,
// typed/untyped defaults, shifts, string/rune/float/complex constants, and
// constants used as array lengths and init-time table values.
package main

const (
	base      = 7
	doubled   = base * 2
	shifted   = 1 << 10
	mask      = shifted - 1
	hello     = "hello"
	helloLen  = len(hello)
	tag       = hello + "!"
	letter    = 'A'
	letterStr = string(letter)
	rounded   = 1.5 * 4
	cval      = complex(2, 3)
	creal     = real(cval)
	cimag     = imag(cval)
	lo        = min(3, 1, 2)
	hi        = max(3, 1, 2)
	arrLen    = int(1 << 4)
	u8max     = uint8(0xff)
)

const (
	modeA = iota
	modeB
	modeC
	modeShift = 1 << (iota + 1)
)

var (
	defInt   = doubled
	defFloat = rounded
	defRune  = letter
	defStr   = tag
	defCplx  = cval
	defU8    = u8max

	// Runtime conversion wraps; the constant argument itself is a plain int.
	narrow = toInt8(doubled * 19)

	table    [arrLen]int
	strTable = [...]string{letterStr, tag, hello}
	initSum  int
)

func toInt8(v int) int8 { return int8(v) }

func init() {
	for i := range table {
		table[i] = i*int(doubled) + int(mask%97) + int(u8max)%7
	}
	for _, v := range table {
		initSum += v
	}
}
