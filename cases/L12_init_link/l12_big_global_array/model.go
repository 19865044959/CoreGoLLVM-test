package main

import (
	"strconv"
	"sync/atomic"
)

const recordLen = 4096

type payload struct {
	data [64]byte
	next *payload
}

type record struct {
	id      int64
	name    string
	tag     [16]byte
	score   int64
	payload *payload
}

var (
	recordBuilds int32
	records      = buildRecords()
	mutated      []*payload
)

// buildRecords runs during package-level variable initialization and fills a
// large global array of pointer-containing structs.
func buildRecords() [recordLen]record {
	var out [recordLen]record
	var prev *payload
	for i := recordLen - 1; i >= 0; i-- {
		p := &payload{next: prev}
		for j := range p.data {
			p.data[j] = byte(i*11 + j)
		}
		out[i] = record{
			id:      int64(i),
			name:    "rec-" + strconv.Itoa(i),
			score:   int64(i)*5 + 7,
			payload: p,
		}
		for j := range out[i].tag {
			out[i].tag[j] = byte(i + j)
		}
		prev = p
	}
	atomic.AddInt32(&recordBuilds, 1)
	return out
}

func touched(i, start int) bool { return i >= start && (i-start)%137 == 0 }

func touchedCount(start int) int {
	return (recordLen-1-start)/137 + 1
}

// validate scans every element of the global array. extra is the number of
// mutation rounds applied to the selected subset.
func validate(rounds, start int) {
	extra := int64(rounds)
	for i := 0; i < recordLen; i++ {
		r := &records[i]
		if r.id != int64(i) {
			ck(false, "record id")
			return
		}
		if r.name != "rec-"+strconv.Itoa(i) {
			ck(false, "record name")
			return
		}
		wantScore := int64(i)*5 + 7
		if touched(i, start) {
			wantScore += extra
		}
		if r.score != wantScore {
			ck(false, "record score")
			return
		}
		if r.payload == nil {
			ck(false, "record payload nil")
			return
		}
		for j := 0; j < len(r.payload.data); j++ {
			if r.payload.data[j] != byte(i*11+j) {
				ck(false, "payload bytes")
				return
			}
		}
		for j := 0; j < 15; j++ {
			if r.tag[j] != byte(i+j) {
				ck(false, "tag bytes")
				return
			}
		}
		wantTag := byte(i + 15)
		if rounds > 0 && touched(i, start) {
			wantTag = byte(rounds)
		}
		if r.tag[15] != wantTag {
			ck(false, "tag mutation")
			return
		}
	}
	p := records[0].payload
	count := 0
	for p != nil {
		count++
		if count > recordLen+1 {
			ck(false, "payload chain cycle")
			return
		}
		p = p.next
	}
	ck(count == recordLen, "payload chain length")

	if len(mutated) != rounds {
		ck(false, "mutated payload count")
		return
	}
	for r := 0; r < rounds; r++ {
		m := mutated[r]
		if m == nil || m.data[0] != byte(200+r) || m.data[63] != byte(200+r+63) {
			ck(false, "mutated payload bytes")
			return
		}
	}
}

func scoreSum(rounds, start int) int64 {
	var sum int64
	for i := 0; i < recordLen; i++ {
		sum += int64(i)*5 + 7
	}
	return sum + int64(rounds)*int64(touchedCount(start))
}
