//go:build tagX && (tagY || !tagY)

package main

func comboImpl() string { return "on" }
