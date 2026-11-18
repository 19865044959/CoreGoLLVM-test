package main

type implNode struct {
	id  int64
	tag string
}

type builder func(int64) *implNode

type entry struct {
	name string
	prio int
	fn   builder
}

var (
	registry     []entry
	builderInits []string
)

func registerBuilder(name string, prio int, fn builder) {
	registry = append(registry, entry{name: name, prio: prio, fn: fn})
}

func plainNode(id int64) *implNode { return &implNode{id: id, tag: "plain"} }

func init() {
	registerBuilder("plain", 5, plainNode)
	builderInits = append(builderInits, "plain")
}

func pickHighest() entry {
	best := registry[0]
	for _, e := range registry[1:] {
		if e.prio > best.prio {
			best = e
		}
	}
	return best
}

type constNode struct{}

func (constNode) Kind() string { return "const" }

func buildWith[T interface{ Kind() string }](v T) string { return "G:" + v.Kind() }
