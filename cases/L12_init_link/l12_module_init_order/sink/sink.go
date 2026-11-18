// Package sink records package-initialization marks for the whole module.
// It is imported by every other package, so its own init must run first.
package sink

var marks []string

// Add appends one initialization mark and returns s unchanged.
func Add(s string) string {
	marks = append(marks, s)
	return s
}

// Marks returns a copy of the trace accumulated so far.
func Marks() []string {
	out := make([]string, len(marks))
	copy(out, marks)
	return out
}

func init() {
	Add("sink.init")
}
