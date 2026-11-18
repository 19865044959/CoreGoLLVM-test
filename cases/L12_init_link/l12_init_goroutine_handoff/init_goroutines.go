package main

import (
	"sync"
	"sync/atomic"
)

type handoff struct {
	chain    []*node
	sum      int64
	initDone bool
}

var (
	startGate = make(chan struct{})
	stopRead  = make(chan struct{})
	handoffCh = make(chan handoff, 1)
	sumCh     = make(chan int64, 1)
	readyCh   = make(chan int, 1)
	initWG    sync.WaitGroup

	initDoneFlag      bool
	initSeenByWorkers int32
)

// init starts three goroutines that block on startGate. main closes the gate
// only after all package initialization has finished, so the channel close
// provides the happens-before edge from init stores to worker loads.
func init() {
	initWG.Add(3)
	go func() {
		defer initWG.Done()
		<-startGate
		if initDoneFlag {
			atomic.AddInt32(&initSeenByWorkers, 1)
		}
		var chain []*node
		sum := int64(0)
		for n := graph.head; n != nil; n = n.next {
			sum += int64(n.score)
			if n.id%3 == 0 {
				chain = append(chain, n)
			}
		}
		handoffCh <- handoff{chain: chain, sum: sum, initDone: initDoneFlag}
	}()
	go func() {
		defer initWG.Done()
		<-startGate
		sum := int64(0)
		for n := graph.head; n != nil; n = n.next {
			sum += int64(n.score)
		}
		sumCh <- sum
		<-stopRead
	}()
	go func() {
		defer initWG.Done()
		<-startGate
		readyCh <- 1
	}()
	initDoneFlag = true
}
