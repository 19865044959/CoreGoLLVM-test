package main

var VM = note("var-m", VTag+1)

// 依赖 VM 的堆对象在变量阶段构造，init 阶段必须能看到它。
var payload = &payloadNode{v: VM, name: "phase-payload"}
