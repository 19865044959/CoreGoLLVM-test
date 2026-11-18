package main

import "strconv"

// validate checks pointer identity across all global tables. extra is the
// score delta applied by the mutation phase. Only builtin entries use the
// index-based identity rules; inserted entries are appended after them.
func validate(ts *tableSet, extra int64, seed int64) {
	n := len(ts.ordered)
	ck(len(ts.byID) == n && len(ts.payloads) == n, "table size consistency")
	if n < builtinCount {
		ck(false, "ordered table shrank")
		return
	}

	// Pointer identity between map, slice and per-entry payload tables.
	for i, e := range ts.ordered {
		if ts.byID[e.id] != e {
			ck(false, "byID identity at "+strconv.Itoa(i))
			return
		}
		if ts.payloads[i][e.name] != e {
			ck(false, "payload table identity at "+strconv.Itoa(i))
			return
		}
		if e.payload == nil {
			ck(false, "nil payload at "+strconv.Itoa(i))
			return
		}
		for j := 0; j < len(e.payload); j++ {
			if e.payload[j] != byte(e.id*7+j) {
				ck(false, "payload bytes at "+strconv.Itoa(i))
				return
			}
		}
		if i < builtinCount {
			if e.id != i || e.name != keyName(i) || e.score != int64(i)*3+1+extra {
				ck(false, "builtin entry fields at "+strconv.Itoa(i))
				return
			}
		} else {
			if e.id < 1000 || e.score != int64(e.id)*3+1+extra {
				ck(false, "inserted entry fields at "+strconv.Itoa(i))
				return
			}
		}
	}

	// Group membership: every entry appears exactly once in its group.
	seen := make(map[*entry]bool, n)
	total := 0
	for g := 0; g < 4; g++ {
		group := ts.groups[groupName(g)]
		total += len(group)
		for _, e := range group {
			if seen[e] || e.id%4 != g || ts.byID[e.id] != e {
				ck(false, "group membership "+groupName(g))
				return
			}
			seen[e] = true
		}
	}
	ck(total == n, "group sizes sum to table size")

	// Pointer-keyed cross table: builtin aliases follow the build formula;
	// inserted entries alias the first two ordered entries.
	for i, e := range ts.ordered {
		aliases := ts.cross[e]
		if len(aliases) != 2 || aliases[0] == nil || aliases[1] == nil {
			ck(false, "cross table shape at "+strconv.Itoa(i))
			return
		}
		if ts.byID[aliases[0].id] != aliases[0] || ts.byID[aliases[1].id] != aliases[1] {
			ck(false, "cross table target identity at "+strconv.Itoa(i))
			return
		}
		if i < builtinCount {
			if aliases[0] != ts.ordered[(i+1)%builtinCount] ||
				aliases[1] != ts.ordered[(i+7)%builtinCount] {
				ck(false, "cross table formula at "+strconv.Itoa(i))
				return
			}
		} else if aliases[0] != ts.ordered[0] || aliases[1] != ts.ordered[1] {
			ck(false, "inserted cross aliases at "+strconv.Itoa(i))
			return
		}
	}

	// Linked list built by package-level initialization plus insertion.
	cur := ts.ordered[0]
	count := 0
	for cur != nil {
		count++
		if count > n+1 {
			ck(false, "linked list cycle")
			return
		}
		cur = cur.next
	}
	ck(count == n, "linked list length")
	ck(ts.tail == ts.ordered[n-1], "tail pointer")

	// Rotating deep checks driven by UAF_SEED.
	start := int(seed % int64(n))
	for i := 0; i < 16; i++ {
		e := ts.ordered[(start+i)%n]
		ck(e.payload[0] == byte(e.id*7), "deep payload check")
		ck(ts.cross[e][0] != nil && ts.cross[e][0].id >= 0, "deep cross check")
	}
}
