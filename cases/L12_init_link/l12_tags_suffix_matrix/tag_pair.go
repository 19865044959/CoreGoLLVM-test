//go:build amd64 || arm64

package main

// Build constraint that stays true across the two 64-bit architecture families.
var tagPairVar = addVar("pair.var")

func init() {
	addInit("pair.init")
}
