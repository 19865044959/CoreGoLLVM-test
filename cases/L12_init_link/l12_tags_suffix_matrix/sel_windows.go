package main

// Must never be selected on linux: the _windows suffix excludes it.
var selWindowsVar = addVar("suffix_windows.var")

func init() {
	addInit("suffix_windows.init")
}
