//go:build darwin

package main

func init() {
	platformInit = "darwin"
	platformInits = append(platformInits, "darwin")
}
