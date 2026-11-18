// l10_repeated_merge_dirty: generated-style wire decoder for repeated and map
// fields decoded *into a dirty destination* (proto merge semantics), with
// adversarial field ordering, packed/unpacked interleaving, duplicate map
// entries, capacity reuse, atomic failure and input-buffer independence.
package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
)

var (
	failed int
	seed   = int64(1)
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(i * 3)
		gcSink = append(gcSink, b)
	}
	for i := range gcSink {
		gcSink[i] = nil
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if failed != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, failed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return def
	}
	return v
}

// ---------- wire primitives ----------

func putUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func readUvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < 10; i++ {
		if i >= len(b) {
			return 0, 0, fmt.Errorf("truncated varint")
		}
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, fmt.Errorf("varint overflow")
		}
		v |= uint64(c&0x7f) << (7 * uint(i))
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, fmt.Errorf("varint overflow")
}

func putTag(b []byte, num int32, wt int) []byte {
	return putUvarint(b, uint64(num)<<3|uint64(wt))
}

func putLen(b []byte, num int32, payload []byte) []byte {
	b = putTag(b, num, 2)
	b = putUvarint(b, uint64(len(payload)))
	return append(b, payload...)
}

func readTag(b []byte) (num int32, wt int, n int, err error) {
	v, n, err := readUvarint(b)
	if err != nil {
		return 0, 0, 0, err
	}
	num = int32(v >> 3)
	wt = int(v & 7)
	if num <= 0 {
		return 0, 0, 0, fmt.Errorf("invalid field number %d", num)
	}
	return num, wt, n, nil
}

func skipValue(b []byte, wt int) (int, error) {
	switch wt {
	case 0:
		_, n, err := readUvarint(b)
		return n, err
	case 1:
		if len(b) < 8 {
			return 0, fmt.Errorf("truncated fixed64")
		}
		return 8, nil
	case 2:
		l, n, err := readUvarint(b)
		if err != nil {
			return 0, err
		}
		if l > uint64(len(b)-n) {
			return 0, fmt.Errorf("truncated length-delimited field")
		}
		return n + int(l), nil
	case 5:
		if len(b) < 4 {
			return 0, fmt.Errorf("truncated fixed32")
		}
		return 4, nil
	default:
		return 0, fmt.Errorf("unsupported wire type %d", wt)
	}
}

// ---------- model ----------

type item struct {
	a        int32
	b        []byte
	bPresent bool
}

func cloneItem(it *item) *item {
	if it == nil {
		return nil
	}
	out := &item{a: it.a, bPresent: it.bPresent}
	if it.b != nil {
		out.b = append([]byte(nil), it.b...)
	}
	return out
}

type msg struct {
	nums   []int32
	names  []string
	counts map[int32]int32
	items  map[string]*item
	list   []*item
	child  *item
}

func cloneMsg(m *msg) *msg {
	if m == nil {
		return &msg{}
	}
	out := &msg{child: cloneItem(m.child)}
	// Preserve capacity so a merge decode can reuse the old backing arrays.
	if m.nums != nil {
		out.nums = make([]int32, len(m.nums), cap(m.nums))
		copy(out.nums, m.nums)
	}
	if m.names != nil {
		out.names = make([]string, len(m.names), cap(m.names))
		copy(out.names, m.names)
	}
	if m.counts != nil {
		out.counts = make(map[int32]int32, len(m.counts))
		for k, v := range m.counts {
			out.counts[k] = v
		}
	}
	if m.items != nil {
		out.items = make(map[string]*item, len(m.items))
		for k, v := range m.items {
			out.items[k] = cloneItem(v)
		}
	}
	if m.list != nil {
		out.list = make([]*item, len(m.list), cap(m.list))
		for i, it := range m.list {
			out.list[i] = cloneItem(it)
		}
	}
	return out
}

func equalItem(a, b *item) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	if a.a != b.a || a.bPresent != b.bPresent {
		return false
	}
	return bytes.Equal(a.b, b.b)
}

func equalMsg(a, b *msg) bool {
	if len(a.nums) != len(b.nums) {
		return false
	}
	for i := range a.nums {
		if a.nums[i] != b.nums[i] {
			return false
		}
	}
	if len(a.names) != len(b.names) {
		return false
	}
	for i := range a.names {
		if a.names[i] != b.names[i] {
			return false
		}
	}
	if (a.counts == nil) != (b.counts == nil) || len(a.counts) != len(b.counts) {
		return false
	}
	for k, v := range a.counts {
		if w, ok := b.counts[k]; !ok || w != v {
			return false
		}
	}
	if (a.items == nil) != (b.items == nil) || len(a.items) != len(b.items) {
		return false
	}
	for k, v := range a.items {
		if !equalItem(v, b.items[k]) {
			return false
		}
	}
	if len(a.list) != len(b.list) {
		return false
	}
	for i := range a.list {
		if !equalItem(a.list[i], b.list[i]) {
			return false
		}
	}
	return equalItem(a.child, b.child)
}

// ---------- decode ----------

func decodeItemPayload(payload []byte, dst *item) error {
	i := 0
	for i < len(payload) {
		num, wt, n, err := readTag(payload[i:])
		if err != nil {
			return err
		}
		i += n
		switch {
		case num == 1 && wt == 0:
			v, m, err := readUvarint(payload[i:])
			if err != nil {
				return err
			}
			i += m
			dst.a = int32(int64(v))
		case num == 2 && wt == 2:
			l, m, err := readUvarint(payload[i:])
			if err != nil {
				return err
			}
			i += m
			if l > uint64(len(payload)-i) {
				return fmt.Errorf("truncated item.b")
			}
			dst.b = make([]byte, int(l))
			copy(dst.b, payload[i:i+int(l)])
			dst.bPresent = true
			i += int(l)
		default:
			m, err := skipValue(payload[i:], wt)
			if err != nil {
				return err
			}
			i += m
		}
	}
	return nil
}

func decodeEntryInt(payload []byte, out map[int32]int32) error {
	var key int32
	var val int32
	i := 0
	for i < len(payload) {
		num, wt, n, err := readTag(payload[i:])
		if err != nil {
			return err
		}
		i += n
		switch {
		case num == 1 && wt == 0:
			v, m, err := readUvarint(payload[i:])
			if err != nil {
				return err
			}
			i += m
			key = int32(int64(v))
		case num == 2 && wt == 0:
			v, m, err := readUvarint(payload[i:])
			if err != nil {
				return err
			}
			i += m
			val = int32(int64(v))
		default:
			m, err := skipValue(payload[i:], wt)
			if err != nil {
				return err
			}
			i += m
		}
	}
	out[key] = val
	return nil
}

func decodeEntryItem(payload []byte, out map[string]*item) (err error) {
	var key string
	var valuePayload []byte
	hasValue := false
	i := 0
	for i < len(payload) {
		num, wt, n, e := readTag(payload[i:])
		if e != nil {
			return e
		}
		i += n
		switch {
		case num == 1 && wt == 2:
			l, m, e := readUvarint(payload[i:])
			if e != nil {
				return e
			}
			i += m
			if l > uint64(len(payload)-i) {
				return fmt.Errorf("truncated map key")
			}
			key = string(payload[i : i+int(l)])
			i += int(l)
		case num == 2 && wt == 2:
			l, m, e := readUvarint(payload[i:])
			if e != nil {
				return e
			}
			i += m
			if l > uint64(len(payload)-i) {
				return fmt.Errorf("truncated map value")
			}
			valuePayload = payload[i : i+int(l)]
			hasValue = true
			i += int(l)
		default:
			m, e := skipValue(payload[i:], wt)
			if e != nil {
				return e
			}
			i += m
		}
	}
	it := &item{}
	if hasValue {
		if err := decodeItemPayload(valuePayload, it); err != nil {
			return err
		}
	}
	// Map duplicate keys replace the value wholesale (no per-field merge).
	out[key] = it
	return nil
}

func decodeMsg(data []byte, dst *msg, merge bool) error {
	out := cloneMsg(dst)
	if !merge {
		out = &msg{}
	}
	i := 0
	for i < len(data) {
		num, wt, n, err := readTag(data[i:])
		if err != nil {
			return err
		}
		i += n
		switch {
		case num == 1 && wt == 2: // packed repeated int32
			l, m, err := readUvarint(data[i:])
			if err != nil {
				return err
			}
			i += m
			if l > uint64(len(data)-i) {
				return fmt.Errorf("truncated packed int32")
			}
			end := i + int(l)
			for i < end {
				v, m, err := readUvarint(data[i:end])
				if err != nil {
					return err
				}
				i += m
				out.nums = append(out.nums, int32(int64(v)))
			}
		case num == 1 && wt == 0: // unpacked repeated int32
			v, m, err := readUvarint(data[i:])
			if err != nil {
				return err
			}
			i += m
			out.nums = append(out.nums, int32(int64(v)))
		case num == 2 && wt == 2:
			l, m, err := readUvarint(data[i:])
			if err != nil {
				return err
			}
			i += m
			if l > uint64(len(data)-i) {
				return fmt.Errorf("truncated string")
			}
			out.names = append(out.names, string(data[i:i+int(l)]))
			i += int(l)
		case num == 3 && wt == 2:
			l, m, err := readUvarint(data[i:])
			if err != nil {
				return err
			}
			i += m
			if l > uint64(len(data)-i) {
				return fmt.Errorf("truncated map entry")
			}
			if out.counts == nil {
				out.counts = map[int32]int32{}
			}
			if err := decodeEntryInt(data[i:i+int(l)], out.counts); err != nil {
				return err
			}
			i += int(l)
		case num == 4 && wt == 2:
			l, m, err := readUvarint(data[i:])
			if err != nil {
				return err
			}
			i += m
			if l > uint64(len(data)-i) {
				return fmt.Errorf("truncated map entry")
			}
			if out.items == nil {
				out.items = map[string]*item{}
			}
			if err := decodeEntryItem(data[i:i+int(l)], out.items); err != nil {
				return err
			}
			i += int(l)
		case num == 5 && wt == 2:
			l, m, err := readUvarint(data[i:])
			if err != nil {
				return err
			}
			i += m
			if l > uint64(len(data)-i) {
				return fmt.Errorf("truncated list item")
			}
			it := &item{}
			if err := decodeItemPayload(data[i:i+int(l)], it); err != nil {
				return err
			}
			out.list = append(out.list, it)
			i += int(l)
		case num == 6 && wt == 2:
			l, m, err := readUvarint(data[i:])
			if err != nil {
				return err
			}
			i += m
			if l > uint64(len(data)-i) {
				return fmt.Errorf("truncated child")
			}
			if out.child == nil {
				out.child = &item{}
			}
			if err := decodeItemPayload(data[i:i+int(l)], out.child); err != nil {
				return err
			}
			i += int(l)
		default:
			if num >= 1 && num <= 6 {
				return fmt.Errorf("field %d: unexpected wire type %d", num, wt)
			}
			m, err := skipValue(data[i:], wt)
			if err != nil {
				return err
			}
			i += m
		}
	}
	*dst = *out
	return nil
}

// ---------- encode ----------

func encodeItemPayload(it *item) []byte {
	var b []byte
	b = putTag(b, 1, 0)
	b = putUvarint(b, uint64(int64(it.a)))
	if it.bPresent {
		b = putLen(b, 2, it.b)
	}
	return b
}

func sortedIntKeys(m map[int32]int32) []int32 {
	keys := make([]int32, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

func sortedItemKeys(m map[string]*item) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func encodeMsg(m *msg, adversarial bool, r *rand.Rand) []byte {
	var b []byte
	if adversarial {
		// Split repeated int32 into alternating packed chunks and unpacked singles.
		pos := 0
		for pos < len(m.nums) {
			if r.Intn(2) == 0 {
				n := 1 + r.Intn(3)
				if pos+n > len(m.nums) {
					n = len(m.nums) - pos
				}
				var payload []byte
				for j := 0; j < n; j++ {
					payload = putUvarint(payload, uint64(int64(m.nums[pos+j])))
				}
				b = putLen(b, 1, payload)
				pos += n
			} else {
				b = putTag(b, 1, 0)
				b = putUvarint(b, uint64(int64(m.nums[pos])))
				pos++
			}
		}
		// Interleave an empty packed segment to exercise the nil/empty boundary.
		if r.Intn(3) == 0 {
			b = putLen(b, 1, nil)
		}
	} else if len(m.nums) > 0 {
		var payload []byte
		for _, v := range m.nums {
			payload = putUvarint(payload, uint64(int64(v)))
		}
		b = putLen(b, 1, payload)
	}
	for _, s := range m.names {
		b = putLen(b, 2, []byte(s))
	}
	for _, k := range sortedIntKeys(m.counts) {
		var e []byte
		e = putTag(e, 1, 0)
		e = putUvarint(e, uint64(int64(k)))
		e = putTag(e, 2, 0)
		e = putUvarint(e, uint64(int64(m.counts[k])))
		// Unknown field inside a map entry must be skipped.
		e = putTag(e, 99, 0)
		e = putUvarint(e, 7)
		if adversarial {
			var stale []byte
			stale = putTag(stale, 1, 0)
			stale = putUvarint(stale, uint64(int64(k)))
			stale = putTag(stale, 2, 0)
			stale = putUvarint(stale, uint64(int64(m.counts[k]))+1000)
			b = putLen(b, 3, stale)
		}
		b = putLen(b, 3, e)
	}
	for _, k := range sortedItemKeys(m.items) {
		it := m.items[k]
		if adversarial {
			// A partial stale entry for the same key: it must be replaced, not merged.
			var stale []byte
			stale = putLen(stale, 1, []byte(k))
			var partial []byte
			partial = putTag(partial, 1, 0)
			partial = putUvarint(partial, uint64(int64(it.a))+7)
			stale = putLen(stale, 2, partial)
			b = putLen(b, 4, stale)
		}
		var e []byte
		e = putLen(e, 1, []byte(k))
		e = putLen(e, 2, encodeItemPayload(it))
		e = putTag(e, 98, 0)
		e = putUvarint(e, 1)
		b = putLen(b, 4, e)
	}
	for _, it := range m.list {
		b = putLen(b, 5, encodeItemPayload(it))
	}
	if m.child != nil {
		if adversarial {
			var onlyA []byte
			onlyA = putTag(onlyA, 1, 0)
			onlyA = putUvarint(onlyA, uint64(int64(m.child.a)))
			b = putLen(b, 6, onlyA)
		}
		b = putLen(b, 6, encodeItemPayload(m.child))
	}
	// Unknown top-level field must be skipped.
	b = putTag(b, 250, 0)
	b = putUvarint(b, 12345)
	return b
}

func newRandomMsg(r *rand.Rand) *msg {
	m := &msg{}
	n := r.Intn(4)
	for i := 0; i < n; i++ {
		m.nums = append(m.nums, int32(r.Intn(200)-100))
	}
	for i := 0; i < r.Intn(3); i++ {
		m.names = append(m.names, fmt.Sprintf("n%d", r.Intn(100)))
	}
	if n := r.Intn(3); n > 0 {
		m.counts = map[int32]int32{}
		for i := 0; i < n; i++ {
			m.counts[int32(r.Intn(5))] = int32(r.Intn(1000))
		}
	}
	if n := r.Intn(3); n > 0 {
		m.items = map[string]*item{}
		for i := 0; i < n; i++ {
			it := &item{a: int32(r.Intn(50))}
			if r.Intn(2) == 0 {
				it.b = []byte(fmt.Sprintf("b%d", r.Intn(20)))
				it.bPresent = true
			}
			m.items[fmt.Sprintf("k%d", r.Intn(5))] = it
		}
	}
	for i := 0; i < r.Intn(3); i++ {
		it := &item{a: int32(r.Intn(50))}
		if r.Intn(2) == 0 {
			it.b = []byte(fmt.Sprintf("l%d", r.Intn(20)))
			it.bPresent = true
		}
		m.list = append(m.list, it)
	}
	if r.Intn(2) == 0 {
		m.child = &item{a: int32(r.Intn(50))}
		if r.Intn(2) == 0 {
			m.child.b = []byte(fmt.Sprintf("c%d", r.Intn(20)))
			m.child.bPresent = true
		}
	}
	return m
}

// mergeLogical applies the same semantics as decodeMsg(..., merge=true).
func mergeLogical(dst, src *msg) {
	dst.nums = append(dst.nums, src.nums...)
	dst.names = append(dst.names, src.names...)
	if len(src.counts) > 0 {
		if dst.counts == nil {
			dst.counts = map[int32]int32{}
		}
		for k, v := range src.counts {
			dst.counts[k] = v
		}
	}
	if len(src.items) > 0 {
		if dst.items == nil {
			dst.items = map[string]*item{}
		}
		for k, v := range src.items {
			dst.items[k] = cloneItem(v)
		}
	}
	for _, it := range src.list {
		dst.list = append(dst.list, cloneItem(it))
	}
	if src.child != nil {
		if dst.child == nil {
			dst.child = cloneItem(src.child)
		} else {
			dst.child.a = src.child.a
			if src.child.bPresent {
				dst.child.b = append([]byte(nil), src.child.b...)
				dst.child.bPresent = true
			}
		}
	}
}

// ---------- tests ----------

func mustDecode(tag string, data []byte, merge bool) *msg {
	m := &msg{}
	if err := decodeMsg(data, m, merge); err != nil {
		ck(false, tag+": "+err.Error())
		return m
	}
	return m
}

func main() {
	seed = readSeed()

	// Crafted payload covering packed/unpacked interleaving, empty packed,
	// negative int32, map defaults/duplicates/unknown entry fields, repeated
	// item, singular child merged across occurrences, and unknown top-level.
	var p []byte
	p = putTag(p, 1, 0)
	p = putUvarint(p, 5)
	var packed []byte
	packed = putUvarint(packed, 6)
	packed = putUvarint(packed, 7)
	p = putLen(p, 1, packed)
	p = putLen(p, 1, nil) // empty packed segment
	p = putTag(p, 1, 0)
	p = putUvarint(p, ^uint64(0)) // -1 as a 10-byte varint
	p = putLen(p, 2, []byte("a"))
	p = putLen(p, 2, []byte("b"))
	// counts: {1:10}, missing key (=>0:20), missing value (=>2:0), dup {1:30}
	var e1 []byte
	e1 = putTag(e1, 1, 0)
	e1 = putUvarint(e1, 1)
	e1 = putTag(e1, 2, 0)
	e1 = putUvarint(e1, 10)
	p = putLen(p, 3, e1)
	var e2 []byte
	e2 = putTag(e2, 2, 0)
	e2 = putUvarint(e2, 20)
	p = putLen(p, 3, e2)
	var e3 []byte
	e3 = putTag(e3, 1, 0)
	e3 = putUvarint(e3, 2)
	p = putLen(p, 3, e3)
	var e4 []byte
	e4 = putTag(e4, 1, 0)
	e4 = putUvarint(e4, 1)
	e4 = putTag(e4, 2, 0)
	e4 = putUvarint(e4, 30)
	e4 = putTag(e4, 77, 0)
	e4 = putUvarint(e4, 1)
	p = putLen(p, 3, e4)
	// items: x {a=1,b="one"}, y (missing value => zero item), x {a=2} (replacement)
	var key []byte
	key = putLen(key, 1, []byte("x"))
	var val []byte
	val = putTag(val, 1, 0)
	val = putUvarint(val, 1)
	val = putLen(val, 2, []byte("one"))
	p = putLen(p, 4, append(append([]byte(nil), key...), putLen(nil, 2, val)...))
	var keyY []byte
	keyY = putLen(keyY, 1, []byte("y"))
	p = putLen(p, 4, keyY)
	var key2 []byte
	key2 = putLen(key2, 1, []byte("x"))
	var val2 []byte
	val2 = putTag(val2, 1, 0)
	val2 = putUvarint(val2, 2)
	key2 = putLen(key2, 2, val2)
	p = putLen(p, 4, key2)
	// list item {a=3,b=""}
	var li []byte
	li = putTag(li, 1, 0)
	li = putUvarint(li, 3)
	li = putLen(li, 2, nil)
	p = putLen(p, 5, li)
	// child occurrences: {a=9}, {b="z"}, {a=10} => {10,"z"}
	var c1 []byte
	c1 = putTag(c1, 1, 0)
	c1 = putUvarint(c1, 9)
	p = putLen(p, 6, c1)
	p = putLen(p, 6, putLen(nil, 2, []byte("z")))
	var c3 []byte
	c3 = putTag(c3, 1, 0)
	c3 = putUvarint(c3, 10)
	p = putLen(p, 6, c3)
	p = putTag(p, 200, 0)
	p = putUvarint(p, 42)

	got := mustDecode("crafted decode", p, false)
	want := &msg{
		nums:   []int32{5, 6, 7, -1},
		names:  []string{"a", "b"},
		counts: map[int32]int32{0: 20, 1: 30, 2: 0},
		items: map[string]*item{
			"x": {a: 2},
			"y": {},
		},
		list:  []*item{{a: 3, b: []byte{}, bPresent: true}},
		child: &item{a: 10, b: []byte("z"), bPresent: true},
	}
	ck(equalMsg(got, want), "crafted payload merge semantics")
	ck(got.items["x"].b == nil && !got.items["x"].bPresent, "map duplicate replaces (no field merge)")
	ck(got.list[0].b != nil && len(got.list[0].b) == 0, "zero-length bytes decoded non-nil")
	ck(got.child.bPresent && string(got.child.b) == "z", "singular child merges across occurrences")

	// Empty packed segment on its own must not allocate a slice.
	mEmpty := mustDecode("empty packed", putLen(nil, 1, nil), false)
	ck(mEmpty.nums == nil, "empty packed leaves repeated nil")

	// Merge into a dirty destination, preserving capacity and unrelated entries.
	base := &msg{
		nums:   make([]int32, 1, 16),
		names:  []string{"base"},
		counts: map[int32]int32{1: 1},
		items: map[string]*item{
			"keep": {a: 1},
		},
		list:  make([]*item, 1, 8),
		child: &item{a: 1, b: []byte("base"), bPresent: true},
	}
	base.nums[0] = 1
	base.list[0] = &item{a: 7}
	oldNumCap := cap(base.nums)
	oldListCap := cap(base.list)

	var add []byte
	var addPacked []byte
	addPacked = putUvarint(addPacked, 2)
	addPacked = putUvarint(addPacked, 3)
	add = putLen(add, 1, addPacked)
	add = putLen(add, 2, []byte("new"))
	var ce []byte
	ce = putTag(ce, 1, 0)
	ce = putUvarint(ce, 1)
	ce = putTag(ce, 2, 0)
	ce = putUvarint(ce, 9)
	add = putLen(add, 3, ce)
	var ce2 []byte
	ce2 = putTag(ce2, 1, 0)
	ce2 = putUvarint(ce2, 2)
	ce2 = putTag(ce2, 2, 0)
	ce2 = putUvarint(ce2, 2)
	add = putLen(add, 3, ce2)
	var ie []byte
	ie = putLen(ie, 1, []byte("new"))
	var iv []byte
	iv = putTag(iv, 1, 0)
	iv = putUvarint(iv, 5)
	ie = putLen(ie, 2, iv)
	add = putLen(add, 4, ie)
	var ladd []byte
	ladd = putTag(ladd, 1, 0)
	ladd = putUvarint(ladd, 8)
	add = putLen(add, 5, ladd)
	var ca []byte
	ca = putTag(ca, 1, 0)
	ca = putUvarint(ca, 5)
	add = putLen(add, 6, ca)
	// Poison the payload after decoding to prove payload copies.
	if err := decodeMsg(add, base, true); err != nil {
		ck(false, "merge decode: "+err.Error())
	}
	for i := range add {
		add[i] = 0xEE
	}
	gcStress()
	ck(len(base.nums) == 3 && base.nums[0] == 1 && base.nums[1] == 2 && base.nums[2] == 3, "merge appends repeated")
	ck(cap(base.nums) >= oldNumCap, "merge reuses/does-not-shrink destination capacity")
	ck(len(base.names) == 2 && base.names[0] == "base" && base.names[1] == "new", "merge appends repeated strings")
	ck(base.counts[1] == 9 && base.counts[2] == 2, "merge overwrites map keys")
	ck(base.items["keep"] != nil && base.items["new"] != nil && base.items["new"].a == 5, "merge preserves old and adds new map entries")
	ck(len(base.list) == 2 && base.list[0].a == 7 && base.list[1].a == 8, "merge appends repeated message")
	ck(cap(base.list) >= oldListCap, "merge reuses list capacity")
	ck(base.child.a == 5 && string(base.child.b) == "base" && base.child.bPresent, "merge child keeps absent source fields")

	// A second source replaces the child payload when it is present.
	cb := putLen(nil, 6, putLen(nil, 2, []byte("upd")))
	var base2 = cloneMsg(base)
	if err := decodeMsg(cb, base2, true); err != nil {
		ck(false, "child replace decode: "+err.Error())
	}
	ck(base2.child.a == 5 && string(base2.child.b) == "upd", "child payload replaced when present")

	// Non-merge decode discards the destination.
	fresh := cloneMsg(base)
	if err := decodeMsg(putLen(nil, 2, []byte("only")), fresh, false); err != nil {
		ck(false, "non-merge decode: "+err.Error())
	}
	ck(len(fresh.names) == 1 && fresh.names[0] == "only" && fresh.counts == nil && fresh.child == nil, "non-merge ignores dirty destination")

	// Atomic failure: destination is unchanged on any decode error.
	stable := cloneMsg(base)
	before := encodeMsg(stable, false, rand.New(rand.NewSource(1)))
	bad := [][]byte{
		{0x0a, 0x05, 0x01},                  // packed length beyond buffer
		{0x0a, 0x02, 0x80},                  // truncated packed varint
		{0x0d, 0x01, 0x02, 0x03, 0x04},      // field 1 with fixed32 wire type
		{0x1a, 0x03, 0x08},                  // truncated map entry
		{0x22, 0x04, 0x0a, 0x01, 'x', 0x12}, // map value truncated
		{0x32, 0x03, 0x08, 0x01},            // child truncated
		{0x0e},                              // wire type 6
		{0x0a, 0x0b, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}, // varint overflow
	}
	for i, b := range bad {
		err := decodeMsg(b, stable, true)
		ck(err != nil, fmt.Sprintf("bad payload %d rejected", i))
	}
	after := encodeMsg(stable, false, rand.New(rand.NewSource(1)))
	ck(bytes.Equal(before, after), "failed decode leaves destination unchanged")
	gcStress()

	// Seeded torture: adversarial encodings, dirty merges, split payloads.
	rounds := envInt("TORTURE_ROUNDS", 200)
	r := rand.New(rand.NewSource(seed))
	okRounds := 0
	for round := 0; round < rounds; round++ {
		logical := newRandomMsg(r)
		payload := encodeMsg(logical, true, r)
		baseRand := newRandomMsg(r)
		wantM := cloneMsg(baseRand)
		mergeLogical(wantM, logical)
		gotM := cloneMsg(baseRand)
		if err := decodeMsg(payload, gotM, true); err != nil {
			ckSeed(false, fmt.Sprintf("torture %d decode: %v", round, err))
			continue
		}
		if !equalMsg(gotM, wantM) {
			ckSeed(false, fmt.Sprintf("torture %d merge mismatch", round))
			continue
		}
		// Split the same logical message into two independently encoded parts
		// and decode sequentially; the result must equal the one-shot merge.
		half := &msg{
			nums:  append([]int32(nil), logical.nums[:len(logical.nums)/2]...),
			names: append([]string(nil), logical.names[:len(logical.names)/2]...),
		}
		rest := cloneMsg(logical)
		rest.nums = rest.nums[len(half.nums):]
		rest.names = rest.names[len(half.names):]
		p1 := encodeMsg(half, true, r)
		p2 := encodeMsg(rest, true, r)
		gotSplit := cloneMsg(baseRand)
		if err := decodeMsg(p1, gotSplit, true); err != nil {
			ckSeed(false, fmt.Sprintf("torture %d split1: %v", round, err))
			continue
		}
		if err := decodeMsg(p2, gotSplit, true); err != nil {
			ckSeed(false, fmt.Sprintf("torture %d split2: %v", round, err))
			continue
		}
		if !equalMsg(gotSplit, wantM) {
			ckSeed(false, fmt.Sprintf("torture %d split merge mismatch", round))
			continue
		}
		if round%23 == 0 {
			gcStress()
		}
		okRounds++
	}
	ck(okRounds == rounds, "torture: all merge rounds matched the model")
	gcStress()

	finish("l10_repeated_merge_dirty")
}
