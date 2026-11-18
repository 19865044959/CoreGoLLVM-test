//go:build linux

package main

func init() {
	platformInit = "linux"
	platformInits = append(platformInits, "linux")
}
