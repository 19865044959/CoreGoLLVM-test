package main

import "sync/atomic"

// gateWait records that the init-started worker exists, then blocks until main
// closes the start gate.
func gateWait(idx int) {
	atomic.AddInt32(&stageStarts[idx], 1)
	<-startGate
	atomic.AddInt32(&gateSeen[idx], 1)
}

func stage1() {
	defer pipeWG.Done()
	gateWait(0)
	for j := range inCh {
		j.v = j.v*3 + 1
		midCh <- j
	}
	close(midCh)
}

func stage2() {
	defer pipeWG.Done()
	gateWait(1)
	for j := range midCh {
		j.v += 2
		outCh <- j
	}
	close(outCh)
}

func stage3() {
	defer pipeWG.Done()
	gateWait(2)
	for j := range outCh {
		resultCh <- j
	}
	close(resultCh)
}

func tokenWorker() {
	defer pipeWG.Done()
	gateWait(3)
	tokenCh <- tokenPtr
}
