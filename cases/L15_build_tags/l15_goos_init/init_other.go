//go:build !linux && !windows && !darwin

package main

func init() {
	platformInit = "other"
	platformInits = append(platformInits, "other")
}
