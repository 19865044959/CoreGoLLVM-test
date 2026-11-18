package main

import (
	"sync"
	"sync/atomic"
)

type entry struct {
	key  string
	val  int64
	buf  []byte
	next *entry
}

type lookupTable struct {
	m    map[string]*entry
	keys []string
	head *entry
}

var (
	tableOnce      sync.Once
	tableInitCount int32
	tableGlobal    *lookupTable
)

// ensureTable 是唯一的懒初始化入口；sync.Once 保证 buildTable 只执行一次。
func ensureTable() *lookupTable {
	tableOnce.Do(func() {
		atomic.AddInt32(&tableInitCount, 1)
		tableGlobal = buildTable(tableSize())
	})
	return tableGlobal
}
