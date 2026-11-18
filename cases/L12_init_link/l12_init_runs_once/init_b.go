package main

import "sync/atomic"

func init() {
	initTrace = append(initTrace, "b#1")
	atomic.AddInt32(&bRuns, 1)
	observeInit()
}
