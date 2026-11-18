package main

// marks is filled by package-level variable initializers and init functions of
// the build-constraint-selected files. Initialization is single-threaded.
var marks []string

func addMark(s string) string {
	marks = append(marks, s)
	return s
}
