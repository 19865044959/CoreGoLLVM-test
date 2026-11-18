//go:build tagSel

package main

const selVariant = "alt"

func (n *implNode) Kind() string { return "kind:" + n.tag }

func (n *implNode) Scale() int64 { return 5 }

func altNode(id int64) *implNode { return &implNode{id: id, tag: selVariant} }

// 选择方法 1：包级函数变量在 tag 文件中被赋成不同构造器。
var NewNode = altNode

func init() {
	registerBuilder(selVariant, 20, altNode)
	builderInits = append(builderInits, selVariant)
}
