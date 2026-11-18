package main

import (
	"strconv"
	"sync"
	"sync/atomic"
)

type cell struct {
	key string
	v   int64
	buf *[32]byte
}

func keyOf(i int) string { return "k" + strconv.Itoa(i) }

func newCell(i int) *cell {
	buf := new([32]byte)
	for j := range buf {
		buf[j] = byte(i + j)
	}
	return &cell{key: keyOf(i), v: int64(i)*11 + 5, buf: buf}
}

var (
	onceA sync.Once
	onceB sync.Once

	buildA int32
	buildB int32

	tableA map[string]*cell
	tableB []*cell

	panicOnce sync.Once
	panicRuns int32
)

func ensureA() map[string]*cell {
	onceA.Do(func() {
		atomic.AddInt32(&buildA, 1)
		m := make(map[string]*cell, 16)
		for i := 0; i < 16; i++ {
			m[keyOf(i)] = newCell(i)
		}
		tableA = m
	})
	return tableA
}

func ensureB() []*cell {
	onceB.Do(func() {
		atomic.AddInt32(&buildB, 1)
		out := make([]*cell, 16)
		for i := range out {
			out[i] = newCell(i)
		}
		tableB = out
	})
	return tableB
}

var (
	slotOnce   []sync.Once
	slotVals   []int64
	slotBuilds []int32
)

func initSlotTable(n int) {
	slotOnce = make([]sync.Once, n)
	slotVals = make([]int64, n)
	slotBuilds = make([]int32, n)
}

// ensureSlot lazily initializes one slot exactly once.
func ensureSlot(i int) int64 {
	slotOnce[i].Do(func() {
		atomic.AddInt32(&slotBuilds[i], 1)
		slotVals[i] = int64(i)*7 + 3
	})
	return slotVals[i]
}

// risky panics inside Once.Do. Per the sync.Once contract, a panicking f is
// still considered complete: later Do calls return without calling f again.
func risky() {
	panicOnce.Do(func() {
		atomic.AddInt32(&panicRuns, 1)
		panic("once panic")
	})
}

func tryRisky() (panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	risky()
	return false
}
