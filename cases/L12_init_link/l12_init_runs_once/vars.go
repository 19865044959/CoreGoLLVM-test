package main

import "sync/atomic"

// 所有 init 函数共享的包级状态：init 运行时这些变量必须已初始化。
var (
	initSeedVal = 2000
	initEpoch   = initSeedVal + 21
	initReady   = true

	initTrace []string

	initBad int32

	aRuns int32
	bRuns int32
	cRuns int32

	afterFirstA int32
)

// observeInit 由各 init 函数调用：确认包级变量已经就绪。
func observeInit() {
	if initEpoch != 2021 || !initReady {
		atomic.AddInt32(&initBad, 1)
	}
}
