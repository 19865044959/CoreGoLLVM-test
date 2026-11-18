package seed

import "l15_module_var_deps/trace"

type Item struct {
	ID    int64
	Label string
	Next  *Item
}

var Base int64 = 6

var Node = &Item{ID: 1, Label: "seed"}

func init() {
	Node.Next = &Item{ID: 2, Label: "linked"}
	trace.Mark("seed")
}
