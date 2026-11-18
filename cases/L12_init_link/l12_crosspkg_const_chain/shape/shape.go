// Package shape consumes unit's constants in its own const expressions and
// array types, i.e. constant propagation across a package boundary.
package shape

import (
	"fmt"

	"l12_crosspkg_const_chain/unit"
)

const (
	ShapeScale  = unit.Scale * 2
	ShapeArea   = unit.MaxLen * 2
	ShapeMask   = unit.Mask
	KindOfShape = unit.KindLarge
)

var (
	// Imported constants are legal array lengths.
	Labels   = buildLabels()
	Defaults [unit.MaxLen]int32

	KindNames = map[unit.Kind]string{
		unit.KindNone:  "none",
		unit.KindSmall: "small",
		unit.KindLarge: "large",
	}

	InitDefaultsOK bool
)

func buildLabels() [unit.MaxLen]string {
	var out [unit.MaxLen]string
	for i := range out {
		out[i] = fmt.Sprintf("k%02d", i)
	}
	return out
}

func init() {
	for i := range Defaults {
		Defaults[i] = int32(i)*int32(unit.Scale) + int32(ShapeScale)
	}
	InitDefaultsOK = Defaults[1] == 48 && Labels[47] == "k47"
}
