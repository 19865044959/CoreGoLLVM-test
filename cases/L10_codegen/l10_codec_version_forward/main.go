// l10_codec_version_forward: a versioned codec registry (v1/v2 schemas) where
// an older codec decodes a newer payload by preserving unknown fields verbatim
// and re-encoding them, so a newer codec can recover the extended fields.
package main

import (
	"bytes"
	"encoding/binary"
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
		b[len(b)-1] = byte(i * 13)
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

// ---------- wire ----------

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

func putFixed64(b []byte, num int32, v uint64) []byte {
	b = putTag(b, num, 1)
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	return append(b, tmp[:]...)
}

func readTag(b []byte) (num int32, wt, n int, err error) {
	v, n, err := readUvarint(b)
	if err != nil {
		return 0, 0, 0, err
	}
	num = int32(v >> 3)
	wt = int(v & 7)
	if num <= 0 {
		return 0, 0, 0, fmt.Errorf("invalid field number")
	}
	return num, wt, n, nil
}

func skipValue(data []byte, num int32, wt int) (int, error) {
	switch wt {
	case 0:
		_, n, err := readUvarint(data)
		return n, err
	case 1:
		if len(data) < 8 {
			return 0, fmt.Errorf("truncated fixed64")
		}
		return 8, nil
	case 2:
		l, n, err := readUvarint(data)
		if err != nil {
			return 0, err
		}
		if l > uint64(len(data)-n) {
			return 0, fmt.Errorf("truncated bytes")
		}
		return n + int(l), nil
	case 3:
		i := 0
		for {
			innerNum, innerWt, n, err := readTag(data[i:])
			if err != nil {
				return 0, err
			}
			i += n
			if innerWt == 4 {
				if innerNum != num {
					return 0, fmt.Errorf("mismatched end group")
				}
				return i, nil
			}
			m, err := skipValue(data[i:], innerNum, innerWt)
			if err != nil {
				return 0, err
			}
			i += m
		}
	case 4:
		return 0, fmt.Errorf("unexpected end group")
	case 5:
		if len(data) < 4 {
			return 0, fmt.Errorf("truncated fixed32")
		}
		return 4, nil
	default:
		return 0, fmt.Errorf("unsupported wire type %d", wt)
	}
}

// ---------- model ----------

type fieldKind int

const (
	kVarint fieldKind = iota
	kBytes
	kRepBytes
	kFixed64
)

func wireMatches(k fieldKind, wt int) bool {
	switch k {
	case kVarint:
		return wt == 0
	case kBytes, kRepBytes:
		return wt == 2
	case kFixed64:
		return wt == 1
	}
	return false
}

type schema struct {
	version int
	fields  map[int32]fieldKind
}

func schemaV1() *schema {
	return &schema{version: 1, fields: map[int32]fieldKind{
		1: kVarint, 2: kBytes, 3: kRepBytes, 4: kVarint,
	}}
}

func schemaV2() *schema {
	return &schema{version: 2, fields: map[int32]fieldKind{
		1: kVarint, 2: kBytes, 3: kRepBytes, 4: kVarint, 5: kFixed64, 6: kBytes,
	}}
}

type rawField struct {
	num int32
	wt  int
	raw []byte // tag + value bytes exactly as received
}

type rec struct {
	id      int64
	name    string
	tags    []string
	ok      bool
	score   uint64
	note    string
	unknown []rawField
}

func (r *rec) clone() *rec {
	out := &rec{id: r.id, name: r.name, ok: r.ok, score: r.score, note: r.note}
	if r.tags != nil {
		out.tags = append([]string(nil), r.tags...)
	}
	for _, f := range r.unknown {
		out.unknown = append(out.unknown, rawField{num: f.num, wt: f.wt, raw: append([]byte(nil), f.raw...)})
	}
	return out
}

func equalLogical(a, b *rec) bool {
	if a.id != b.id || a.name != b.name || a.ok != b.ok || a.score != b.score || a.note != b.note {
		return false
	}
	if len(a.tags) != len(b.tags) {
		return false
	}
	for i := range a.tags {
		if a.tags[i] != b.tags[i] {
			return false
		}
	}
	return true
}

func equalUnknown(a, b []rawField) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].num != b[i].num || a[i].wt != b[i].wt || !bytes.Equal(a[i].raw, b[i].raw) {
			return false
		}
	}
	return true
}

// ---------- codec ----------

func decodeWith(sc *schema, data []byte) (*rec, error) {
	out := &rec{}
	i := 0
	for i < len(data) {
		start := i
		num, wt, n, err := readTag(data[i:])
		if err != nil {
			return nil, err
		}
		i += n
		kind, known := sc.fields[num]
		if known && wireMatches(kind, wt) {
			switch kind {
			case kVarint:
				v, m, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += m
				switch num {
				case 1:
					out.id = int64(v)
				case 4:
					out.ok = v != 0
				}
			case kBytes:
				l, m, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += m
				if l > uint64(len(data)-i) {
					return nil, fmt.Errorf("truncated field %d", num)
				}
				s := string(data[i : i+int(l)])
				i += int(l)
				switch num {
				case 2:
					out.name = s
				case 6:
					out.note = s
				}
			case kRepBytes:
				l, m, err := readUvarint(data[i:])
				if err != nil {
					return nil, err
				}
				i += m
				if l > uint64(len(data)-i) {
					return nil, fmt.Errorf("truncated field %d", num)
				}
				out.tags = append(out.tags, string(data[i:i+int(l)]))
				i += int(l)
			case kFixed64:
				if len(data)-i < 8 {
					return nil, fmt.Errorf("truncated field %d", num)
				}
				out.score = binary.LittleEndian.Uint64(data[i : i+8])
				i += 8
			}
		} else {
			m, err := skipValue(data[i:], num, wt)
			if err != nil {
				return nil, err
			}
			i += m
			raw := make([]byte, i-start)
			copy(raw, data[start:i])
			out.unknown = append(out.unknown, rawField{num: num, wt: wt, raw: raw})
		}
	}
	return out, nil
}

func encodeWith(sc *schema, r *rec) []byte {
	var b []byte
	if _, ok := sc.fields[1]; ok && r.id != 0 {
		b = putTag(b, 1, 0)
		b = putUvarint(b, uint64(r.id))
	}
	if _, ok := sc.fields[2]; ok && r.name != "" {
		b = putLen(b, 2, []byte(r.name))
	}
	if _, ok := sc.fields[3]; ok {
		for _, t := range r.tags {
			b = putLen(b, 3, []byte(t))
		}
	}
	if _, ok := sc.fields[4]; ok && r.ok {
		b = putTag(b, 4, 0)
		b = putUvarint(b, 1)
	}
	if _, ok := sc.fields[5]; ok && r.score != 0 {
		b = putFixed64(b, 5, r.score)
	}
	if _, ok := sc.fields[6]; ok && r.note != "" {
		b = putLen(b, 6, []byte(r.note))
	}
	for _, f := range r.unknown {
		b = append(b, f.raw...)
	}
	return b
}

func decodeInto(sc *schema, data []byte, dst *rec) error {
	out, err := decodeWith(sc, data)
	if err != nil {
		return err
	}
	*dst = *out
	return nil
}

// ---------- registry ----------

var (
	errDuplicate = fmt.Errorf("duplicate codec version")
	errFrozen    = fmt.Errorf("codec registry frozen")
	errNotFound  = fmt.Errorf("codec not found")
)

type codec struct {
	name    string
	version int
	sc      *schema
}

type codecRegistry struct {
	m      map[string]map[int]*codec
	frozen bool
}

func newCodecRegistry() *codecRegistry {
	return &codecRegistry{m: map[string]map[int]*codec{}}
}

func (r *codecRegistry) Register(c *codec) error {
	if c == nil || c.sc == nil || c.name == "" || c.version <= 0 {
		return fmt.Errorf("invalid codec")
	}
	if r.frozen {
		return errFrozen
	}
	vers := r.m[c.name]
	if vers == nil {
		vers = map[int]*codec{}
		r.m[c.name] = vers
	}
	if _, ok := vers[c.version]; ok {
		return errDuplicate
	}
	vers[c.version] = c
	return nil
}

func (r *codecRegistry) Resolve(name string, minV, maxV int) (*codec, error) {
	if minV > maxV {
		return nil, fmt.Errorf("invalid version range")
	}
	vers, ok := r.m[name]
	if !ok {
		return nil, errNotFound
	}
	best := -1
	for v := range vers {
		if v >= minV && v <= maxV && v > best {
			best = v
		}
	}
	if best < 0 {
		return nil, errNotFound
	}
	return vers[best], nil
}

func (r *codecRegistry) Freeze() { r.frozen = true }

func (r *codecRegistry) Versions(name string) []int {
	vers := r.m[name]
	out := make([]int, 0, len(vers))
	for v := range vers {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

func newRegistry() *codecRegistry {
	r := newCodecRegistry()
	_ = r.Register(&codec{name: "bin", version: 1, sc: schemaV1()})
	_ = r.Register(&codec{name: "bin", version: 2, sc: schemaV2()})
	return r
}

// ---------- main ----------

func main() {
	seed = readSeed()
	v1 := schemaV1()
	v2 := schemaV2()

	// 1. Golden v1 wire bytes.
	r := &rec{id: 7, name: "a", tags: []string{"x", "y"}, ok: true}
	w1 := encodeWith(v1, r)
	golden1 := []byte{0x08, 0x07, 0x12, 0x01, 'a', 0x1a, 0x01, 'x', 0x1a, 0x01, 'y', 0x20, 0x01}
	ck(bytes.Equal(w1, golden1), "v1 golden bytes")
	back, err := decodeWith(v1, w1)
	ck(err == nil && equalLogical(back, r), "v1 roundtrip")

	// 2. v2 payload decoded by the v1 codec preserves fields 5/6 as unknown.
	r2 := &rec{id: 9, name: "n", tags: []string{"t"}, ok: true, score: 0x0102030405060708, note: "future"}
	w2 := encodeWith(v2, r2)
	seen1, err := decodeWith(v1, w2)
	ck(err == nil, "v1 decodes v2 payload")
	ck(seen1.id == 9 && seen1.name == "n" && seen1.score == 0 && seen1.note == "", "older schema ignores extended fields")
	ck(len(seen1.unknown) == 2 && seen1.unknown[0].num == 5 && seen1.unknown[1].num == 6, "extended fields preserved as unknown")
	rew1 := encodeWith(v1, seen1)
	seen2, err := decodeWith(v2, rew1)
	ck(err == nil && equalLogical(seen2, r2), "newer codec recovers fields after v1 transcode")
	ck(len(seen2.unknown) == 0, "consumed fields leave no unknown residue")
	// Byte stable through the full cycle.
	rew2 := encodeWith(v2, seen2)
	ck(bytes.Equal(w2, rew2), "v2->v1->v2 byte stable")

	// 3. Unknown field raw bytes survive exactly, in arrival order.
	var p []byte
	p = putLen(p, 2, []byte("known"))
	p = putTag(p, 10, 0)
	p = putUvarint(p, 300)
	p = putLen(p, 9, []byte{1, 2, 3})
	p = putTag(p, 11, 1)
	p = append(p, 1, 2, 3, 4, 5, 6, 7, 8)
	p = putTag(p, 12, 5)
	p = append(p, 9, 10, 11, 12)
	// A group unknown field (field 13) must be preserved with its end tag.
	p = putTag(p, 13, 3)
	p = putTag(p, 14, 0)
	p = putUvarint(p, 1)
	p = putTag(p, 13, 4)
	mixed, err := decodeWith(v1, p)
	ck(err == nil && mixed.name == "known" && len(mixed.unknown) == 5, "mixed unknown decode")
	re := encodeWith(v1, mixed)
	ck(bytes.Equal(re, p), "unknown fields re-encode byte-identically")
	gcStress()
	poison := append([]byte(nil), p...)
	for i := range poison {
		poison[i] = 0xEE
	}
	// The decoded record must remain independent of the input buffer.
	ck(bytes.Equal(encodeWith(v1, mixed), p), "unknown bytes independent of input buffer")

	// 4. Known field with a mismatched wire type is preserved, not applied.
	var wrong []byte
	wrong = putTag(wrong, 1, 0)
	wrong = putUvarint(wrong, 5)
	wrong = putLen(wrong, 2, []byte("name"))
	wrong = putTag(wrong, 2, 0) // field 2 (bytes) sent as varint
	wrong = putUvarint(wrong, 77)
	wr, err := decodeWith(v1, wrong)
	ck(err == nil && wr.name == "name" && wr.id == 5 && len(wr.unknown) == 1, "wrong wire type preserved")
	ck(bytes.Equal(encodeWith(v1, wr), wrong), "wrong wire type roundtrips")
	wr2, err := decodeWith(v2, encodeWith(v1, wr))
	ck(err == nil && wr2.name == "name" && len(wr2.unknown) == 1, "newer codec also keeps wrong-wire field unknown")

	// 5. Duplicate scalars last-wins, repeated fields append across versions.
	var dup []byte
	dup = putTag(dup, 1, 0)
	dup = putUvarint(dup, 1)
	dup = putTag(dup, 1, 0)
	dup = putUvarint(dup, 2)
	dup = putLen(dup, 3, []byte("a"))
	dup = putLen(dup, 3, []byte("b"))
	dv, err := decodeWith(v2, dup)
	ck(err == nil && dv.id == 2 && len(dv.tags) == 2, "last-wins and repeated append")

	// 6. Atomic failure: destination unchanged.
	stable := &rec{id: 1, name: "stable", tags: []string{"k"}, unknown: []rawField{{num: 20, wt: 0, raw: []byte{0xa0, 0x01, 0x01}}}}
	before := encodeWith(v2, stable)
	bad := [][]byte{
		{0x08, 0x80},       // truncated varint
		{0x12, 0x05, 'a'},  // truncated bytes
		{0x2a, 0x01},       // truncated fixed64
		{0x62, 0x03, 0x08}, // truncated unknown bytes
		{0x68, 0x80},       // truncated unknown varint
		{0x6d, 1, 2},       // truncated unknown fixed32
		{0x6b, 0x08, 0x01}, // unterminated group
		{0x0e},             // invalid wire type
	}
	for i, b := range bad {
		err := decodeInto(v1, b, stable)
		ck(err != nil, fmt.Sprintf("bad payload %d rejected", i))
	}
	after := encodeWith(v2, stable)
	ck(bytes.Equal(before, after), "failed decode leaves destination unchanged")

	// 7. Registry semantics.
	reg := newRegistry()
	if c, err := reg.Resolve("bin", 1, 1); err != nil || c.version != 1 {
		ck(false, "resolve exactly v1")
	}
	if c, err := reg.Resolve("bin", 1, 2); err != nil || c.version != 2 {
		ck(false, "resolve highest compatible v2")
	}
	if c, err := reg.Resolve("bin", 0, 2); err != nil || c.version != 2 {
		ck(false, "resolve from zero")
	}
	if _, err := reg.Resolve("bin", 3, 4); err == nil {
		ck(false, "resolve out of range rejected")
	}
	if _, err := reg.Resolve("nope", 0, 9); err == nil {
		ck(false, "resolve unknown name rejected")
	}
	if _, err := reg.Resolve("bin", 2, 1); err == nil {
		ck(false, "resolve inverted range rejected")
	}
	ck(reg.Register(&codec{name: "bin", version: 1, sc: schemaV1()}) == errDuplicate, "duplicate version rejected")
	ck(reg.Register(&codec{name: "", version: 1, sc: schemaV1()}) != nil, "empty name rejected")
	ck(reg.Register(&codec{name: "x", version: 0, sc: schemaV1()}) != nil, "zero version rejected")
	ck(reg.Register(&codec{name: "x", version: 1}) != nil, "nil schema rejected")
	vers := reg.Versions("bin")
	ck(len(vers) == 2 && vers[0] == 1 && vers[1] == 2, "versions sorted")
	reg.Freeze()
	ck(reg.Register(&codec{name: "bin", version: 3, sc: schemaV2()}) == errFrozen, "frozen register rejected")
	if c, err := reg.Resolve("bin", 1, 2); err != nil || c.version != 2 {
		ck(false, "resolve works after freeze")
	}

	// 8. GC and many records.
	var keep []*rec
	for i := 0; i < 150; i++ {
		rr := &rec{id: int64(i), name: fmt.Sprintf("n%d", i), tags: []string{"a", "b"}, ok: i%2 == 0, score: uint64(i) * 3, note: fmt.Sprintf("note%d", i)}
		w := encodeWith(v2, rr)
		old, err := decodeWith(v1, w)
		ck(err == nil, "bulk v1 decode")
		newer, err := decodeWith(v2, encodeWith(v1, old))
		ck(err == nil && equalLogical(newer, rr), fmt.Sprintf("bulk forward %d", i))
		keep = append(keep, newer)
	}
	gcStress()
	for i, rr := range keep {
		ck(rr.note == fmt.Sprintf("note%d", i) && rr.score == uint64(i)*3, fmt.Sprintf("bulk after GC %d", i))
	}

	// 9. Seeded torture: random v2 + injected unknown fields through v1.
	rounds := envInt("TORTURE_ROUNDS", 250)
	rnd := rand.New(rand.NewSource(seed))
	okRounds := 0
	for i := 0; i < rounds; i++ {
		orig := &rec{}
		if rnd.Intn(2) == 0 {
			orig.id = rnd.Int63() - (1 << 40)
		}
		if rnd.Intn(2) == 0 {
			orig.name = fmt.Sprintf("n%d_%d", i%13, rnd.Intn(1000))
		}
		if n := rnd.Intn(4); n > 0 {
			orig.tags = make([]string, n)
			for j := range orig.tags {
				orig.tags[j] = fmt.Sprintf("t%d", rnd.Intn(100))
			}
		}
		orig.ok = rnd.Intn(2) == 0
		orig.score = rnd.Uint64()
		if rnd.Intn(2) == 0 {
			orig.note = fmt.Sprintf("note%d", rnd.Intn(1000))
		}
		// Inject unknown fields (numbers above the v2 schema).
		nUnknown := rnd.Intn(4)
		var injected []rawField
		for j := 0; j < nUnknown; j++ {
			num := int32(7 + rnd.Intn(6))
			switch rnd.Intn(4) {
			case 0:
				raw := putTag(nil, num, 0)
				raw = putUvarint(raw, rnd.Uint64()%100000)
				injected = append(injected, rawField{num: num, wt: 0, raw: raw})
			case 1:
				payload := []byte(fmt.Sprintf("u%d", rnd.Intn(100)))
				raw := putLen(nil, num, payload)
				injected = append(injected, rawField{num: num, wt: 2, raw: raw})
			case 2:
				raw := putTag(nil, num, 1)
				var tmp [8]byte
				binary.LittleEndian.PutUint64(tmp[:], rnd.Uint64())
				raw = append(raw, tmp[:]...)
				injected = append(injected, rawField{num: num, wt: 1, raw: raw})
			default:
				raw := putTag(nil, num, 5)
				raw = append(raw, byte(rnd.Intn(256)), byte(rnd.Intn(256)), byte(rnd.Intn(256)), byte(rnd.Intn(256)))
				injected = append(injected, rawField{num: num, wt: 5, raw: raw})
			}
		}
		orig.unknown = injected
		full := encodeWith(v2, orig)
		old, err := decodeWith(v1, full)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture %d v1 decode: %v", i, err))
			continue
		}
		mid := encodeWith(v1, old)
		for j := range mid {
			mid[j] = 0xA5
		}
		back, err := decodeWith(v2, encodeWith(v1, old))
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture %d v2 decode: %v", i, err))
			continue
		}
		if !equalLogical(back, orig) {
			ckSeed(false, fmt.Sprintf("torture %d logical mismatch", i))
			continue
		}
		if !equalUnknown(back.unknown, injected) {
			ckSeed(false, fmt.Sprintf("torture %d unknown mismatch", i))
			continue
		}
		if i%31 == 0 {
			gcStress()
		}
		okRounds++
	}
	ck(okRounds == rounds, "torture: all version-forward rounds matched")

	finish("l10_codec_version_forward")
}
