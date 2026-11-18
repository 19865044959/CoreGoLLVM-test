//go:build !tagRed && !tagBlue

package main

func init() { variantInits = append(variantInits, "base") }

func variantImpl() string { return "base" }
