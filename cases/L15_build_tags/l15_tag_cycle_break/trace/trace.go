package trace

var seq []string

// Mark 记录包级 init 顺序。
func Mark(name string) { seq = append(seq, name) }

func init() { Mark("trace") }

func Snapshot() []string { return append([]string(nil), seq...) }
