//go:build tagBlue && !tagRed

package main

func init() { variantInits = append(variantInits, "blue") }

func variantImpl() string { return "blue" }
