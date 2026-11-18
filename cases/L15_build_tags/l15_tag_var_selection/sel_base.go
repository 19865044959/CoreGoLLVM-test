//go:build !tagSel

package main

const selVariant = "base"

func (n *implNode) Kind() string { return "kind:" + n.tag }

func (n *implNode) Scale() int64 { return 2 }

func baseNode(id int64) *implNode { return &implNode{id: id, tag: selVariant} }

// 选择方法 1：包级函数变量在 tag 文件中被赋成不同构造器。
var NewNode = baseNode

func init() {
	registerBuilder(selVariant, 10, baseNode)
	builderInits = append(builderInits, selVariant)
}
