package main

func (r *rect) Name() string { return "rect" }

// namedCircle 通过嵌入获得 Area/Perimeter，并覆盖 Name。
type namedCircle struct {
	circle
	label string
}

func (n namedCircle) Name() string { return n.label }
