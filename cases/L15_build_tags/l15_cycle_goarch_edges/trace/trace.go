package trace

var seq []string

// Mark 记录包级 init 出现的顺序。
func Mark(name string) { seq = append(seq, name) }

func init() { Mark("trace") }

// Snapshot 返回 init 序列的副本。
func Snapshot() []string { return append([]string(nil), seq...) }
