package main

import (
	"os"
	"strconv"
)

func tableSize() int {
	n := 256
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n *= v
			if n > 65536 {
				n = 65536
			}
		}
	}
	return n
}

func keyOf(i int) string { return "k" + strconv.Itoa(i) }

func makeEntry(i int) *entry {
	buf := make([]byte, 24)
	for j := range buf {
		buf[j] = byte(i + j)
	}
	return &entry{key: keyOf(i), val: int64(i)*3 + 7, buf: buf}
}

func buildTable(n int) *lookupTable {
	t := &lookupTable{
		m:    make(map[string]*entry, n),
		keys: make([]string, 0, n),
	}
	var head *entry
	for i := 0; i < n; i++ {
		e := makeEntry(i)
		e.next = head
		head = e
		t.m[e.key] = e
		t.keys = append(t.keys, e.key)
	}
	t.head = head
	return t
}
