//go:build tagRed && !tagBlue

package main

func init() { variantInits = append(variantInits, "red") }

func variantImpl() string { return "red" }
