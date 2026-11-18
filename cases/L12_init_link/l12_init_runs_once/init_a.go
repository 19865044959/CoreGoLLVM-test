package main

import "sync/atomic"

// 同一文件内两个 init：按源码出现顺序执行，顺序有定义。
func init() {
	initTrace = append(initTrace, "a#1")
	atomic.AddInt32(&aRuns, 1)
	observeInit()
}

func init() {
	initTrace = append(initTrace, "a#2")
	if atomic.LoadInt32(&aRuns) == 1 {
		atomic.StoreInt32(&afterFirstA, 1)
	}
	observeInit()
}
