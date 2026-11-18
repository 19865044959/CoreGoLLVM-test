package main

// Must never be selected on amd64: the _386 suffix excludes it.
var sel386Var = addVar("suffix_386.var")

func init() {
	addInit("suffix_386.init")
}
