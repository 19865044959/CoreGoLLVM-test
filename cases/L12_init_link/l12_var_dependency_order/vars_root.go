package main

// 依赖链的根：本身无依赖。文件名在词法顺序上排在引用者之后，
// 用于确认初始化按依赖序而非文件序执行。

type node struct {
	v     int
	label string
}

var c = 3
var base = 100

var seedNode = node{v: base, label: "seed"}
var seedCopy = seedNode
var seedPtr = &seedNode
var seedAlias = seedPtr
