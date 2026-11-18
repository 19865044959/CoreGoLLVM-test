//go:build windows

package main

func init() {
	platformInit = "windows"
	platformInits = append(platformInits, "windows")
}
