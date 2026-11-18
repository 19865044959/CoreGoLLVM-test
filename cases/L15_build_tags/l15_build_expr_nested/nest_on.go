//go:build (tagA && (tagB || tagC)) || (tagD && !tagA)

package main

func nestImpl() string { return "complex" }
