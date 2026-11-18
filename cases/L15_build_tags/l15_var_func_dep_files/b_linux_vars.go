//go:build linux

package main

func tagBias() int64 { return 2 }

// viaTagged 的函数体引用 C 与 tagBias：Tagged 依赖二者。
func viaTagged() int64 { return C + tagBias() }

var Tagged = note("tagged-linux", viaTagged()+1)

func init() {
	initSawVars = A == 5 && B == 6 && C == 13 && Tagged == 16
	note("init-tag", 0)
}
