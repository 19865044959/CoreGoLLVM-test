package main

import "sync/atomic"

func init() {
	initTrace = append(initTrace, "c#1")
	atomic.AddInt32(&cRuns, 1)
	observeInit()
}
