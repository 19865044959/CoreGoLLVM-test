package main

import "sort"

var seen []string

func mark(s string) { seen = append(seen, s) }

func snapshot() []string {
	out := append([]string(nil), seen...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
