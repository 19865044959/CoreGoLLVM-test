//go:build tagRed && tagBlue

package main

func init() { variantInits = append(variantInits, "both") }

func variantImpl() string { return "both" }
