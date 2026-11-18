package trace

var seq []string

func Mark(s string) { seq = append(seq, s) }

func Snapshot() []string { return append([]string(nil), seq...) }

func init() { Mark("trace") }
