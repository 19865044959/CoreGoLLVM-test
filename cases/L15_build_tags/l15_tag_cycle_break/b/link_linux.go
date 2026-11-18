//go:build linux

package b

func Helper() string { return "b-local:" + LocalB() }
