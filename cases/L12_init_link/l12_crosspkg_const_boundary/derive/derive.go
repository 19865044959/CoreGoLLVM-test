// Package derive consumes limits' constants in its own const expressions and
// array types, i.e. constant propagation across a package boundary.
package derive

import (
	"fmt"

	"l12_crosspkg_const_boundary/limits"
)

const (
	Slots          = limits.Slots * 2
	Span           = limits.Total - limits.Slots
	DerivedName    = limits.Name + "/d"
	DerivedNameLen = len(DerivedName)
	LowMask        = limits.FlagsAll & 0x00ff // stays typed limits.Flags
	LevelSpan      = int(limits.MaxWidth) - int(limits.MinLevel)
	ShiftCheck     = limits.HighBit >> 8
)

var (
	// Constants imported from another package are legal array lengths.
	Grid   [limits.Blocks]uint16
	Zero   [limits.ZeroLen]int
	Rows   [Slots][2]uint8
	Labels [Slots]string

	InitOK bool
)

func init() {
	for i := range Grid {
		Grid[i] = uint16(i)*3 + uint16(limits.HighBit>>10)
	}
	for i := range Rows {
		Rows[i][0] = uint8(i)
		Rows[i][1] = uint8(i*3 + int(LowMask))
	}
	for i := range Labels {
		Labels[i] = fmt.Sprintf("r%02d", i)
	}
	InitOK = len(Grid) == 64 && len(Zero) == 0 && len(Rows) == 34 &&
		len(Labels) == 34 && Labels[33] == "r33" && Grid[63] == uint16(63*3+32)
}
