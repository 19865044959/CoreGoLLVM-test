// L10: 生成代码的字符串表：名称表去重/稳定 ID、哈希桶扩容、冻结、
// JSON 名推导、快照隔离、按字节精确存储与 GC 后查询。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

var (
	failed  int32
	gcSink  [][]byte
	curSeed int64
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Printf("FAIL: %s (seed=%d)\n", msg, curSeed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

var errFrozen = errors.New("string table frozen")

// ---- 字符串表 ----

type stringEntry struct {
	s  string
	id uint32
}

type stringTable struct {
	buckets  [][]stringEntry
	values   []string
	capacity int
	frozen   bool
}

func newStringTable(capacity int) *stringTable {
	if capacity < 4 {
		capacity = 4
	}
	// 取不小于 capacity 的 2 的幂。
	c := 1
	for c < capacity {
		c <<= 1
	}
	return &stringTable{buckets: make([][]stringEntry, c), capacity: c}
}

func hashString(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func (t *stringTable) find(s string) (uint32, bool) {
	idx := hashString(s) & uint64(t.capacity-1)
	for _, e := range t.buckets[idx] {
		if e.s == s {
			return e.id, true
		}
	}
	return 0, false
}

func (t *stringTable) grow() {
	old := t.buckets
	t.capacity *= 2
	t.buckets = make([][]stringEntry, t.capacity)
	for _, bucket := range old {
		for _, e := range bucket {
			idx := hashString(e.s) & uint64(t.capacity-1)
			t.buckets[idx] = append(t.buckets[idx], e)
		}
	}
}

func (t *stringTable) Intern(s string) (uint32, error) {
	if id, ok := t.find(s); ok {
		return id, nil
	}
	if t.frozen {
		return 0, errFrozen
	}
	if (len(t.values)+1)*10 >= t.capacity*7 {
		t.grow()
	}
	id := uint32(len(t.values))
	t.values = append(t.values, s)
	idx := hashString(s) & uint64(t.capacity-1)
	t.buckets[idx] = append(t.buckets[idx], stringEntry{s: s, id: id})
	return id, nil
}

func (t *stringTable) Lookup(s string) (uint32, bool) { return t.find(s) }

func (t *stringTable) ByID(id uint32) (string, bool) {
	if int(id) >= len(t.values) {
		return "", false
	}
	return t.values[id], true
}

func (t *stringTable) Len() int { return len(t.values) }

func (t *stringTable) Freeze() { t.frozen = true }

func (t *stringTable) Snapshot() []string {
	return append([]string(nil), t.values...)
}

// jsonName 对应 protoc-gen-go 的 JSON 名推导：去掉下划线并大写后继字母。
func jsonName(s string) string {
	var b strings.Builder
	upper := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' {
			upper = true
			continue
		}
		if upper && c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		upper = false
		b.WriteByte(c)
	}
	return b.String()
}

func main() {
	seed := int64(1)
	if v := os.Getenv("UAF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	curSeed = seed

	// 1. 基本去重与稳定 ID（首次出现顺序）。
	t := newStringTable(4)
	names := []string{"", "alpha", "beta", "alpha", "field_name", "fieldName", "beta", ""}
	ids := make([]uint32, len(names))
	for i, s := range names {
		id, err := t.Intern(s)
		ck(err == nil, "intern "+s)
		ids[i] = id
	}
	ck(t.Len() == 5, "unique string count")
	ck(ids[0] == ids[7] && ids[1] == ids[3] && ids[2] == ids[6], "duplicate strings share id")
	ck(ids[0] == 0 && ids[1] == 1 && ids[2] == 2, "ids assigned in first-seen order")
	if got, ok := t.ByID(ids[4]); !ok || got != "field_name" {
		ck(false, "byID returns original string")
	}
	if _, ok := t.ByID(99); ok {
		ck(false, "byID out of range")
	}
	if id, ok := t.Lookup("missing"); ok || id != 0 {
		ck(false, "lookup missing")
	}

	// 2. 快照隔离：后续插入不影响快照，修改快照不影响表。
	snap := t.Snapshot()
	before := t.Len()
	if _, err := t.Intern("gamma"); err != nil {
		ck(false, "intern gamma")
	}
	ck(len(snap) == before, "snapshot length unchanged")
	snap[1] = "MUTATED"
	if got, _ := t.ByID(1); got != "alpha" {
		ck(false, "snapshot mutation isolated")
	}

	// 3. 冻结：已有字符串仍可 Intern/查询，新字符串返回 ErrFrozen。
	t.Freeze()
	if id, err := t.Intern("alpha"); err != nil || id != 1 {
		ck(false, "frozen table serves existing string")
	}
	if _, err := t.Intern("delta"); err != errFrozen {
		ck(false, "frozen table rejects new string")
	}
	if _, ok := t.Lookup("delta"); ok {
		ck(false, "frozen rejected string not inserted")
	}

	// 4. 按字节精确存储：NUL、非法 UTF-8、大小写、长字符串不归一化。
	bt := newStringTable(8)
	raw := []string{"a\x00b", "A\x00B", "a\x00b", "\xff\xfe", "ß", "SS", strings.Repeat("x", 65536)}
	rawIDs := make([]uint32, len(raw))
	for i, s := range raw {
		id, err := bt.Intern(s)
		ck(err == nil, "intern raw")
		rawIDs[i] = id
	}
	ck(rawIDs[0] == rawIDs[2], "NUL string deduplicated")
	ck(rawIDs[0] != rawIDs[1], "case-sensitive bytes")
	ck(rawIDs[3] != rawIDs[4] && rawIDs[4] != rawIDs[5], "invalid UTF-8 / unicode distinct")
	if got, _ := bt.ByID(rawIDs[5]); got != "SS" {
		ck(false, "ASCII string exact")
	}
	if got, _ := bt.ByID(rawIDs[6]); len(got) != 65536 || got[0] != 'x' {
		ck(false, "long string exact")
	}
	// 长字符串键的查询必须走内容比较，不能只看长度/前缀。
	if id, ok := bt.Lookup(strings.Repeat("x", 65535) + "y"); ok || id != 0 {
		ck(false, "long near-miss string absent")
	}

	// 5. 扩容：小容量插入大量符号，全部可查且 ID 唯一。
	big := newStringTable(4)
	const n = 4096
	bigIDs := make([]uint32, n)
	for i := 0; i < n; i++ {
		s := fmt.Sprintf("sym_%04d_payload", i)
		id, err := big.Intern(s)
		if err != nil {
			ck(false, "big intern")
		}
		bigIDs[i] = id
	}
	ck(big.Len() == n, "big table length")
	seen := make(map[uint32]struct{}, n)
	for i := 0; i < n; i++ {
		id, ok := big.Lookup(fmt.Sprintf("sym_%04d_payload", i))
		if !ok || id != bigIDs[i] {
			ck(false, "big lookup id")
		}
		seen[id] = struct{}{}
		if got, ok := big.ByID(id); !ok || got != fmt.Sprintf("sym_%04d_payload", i) {
			ck(false, "big byID")
		}
	}
	ck(len(seen) == n, "big ids unique")

	// 6. JSON 名推导表（与枚举/字段名一并入表去重）。
	jsonCases := []struct{ in, want string }{
		{"foo_bar", "fooBar"},
		{"foo__bar", "fooBar"},
		{"_foo", "Foo"},
		{"Foo_bar", "FooBar"},
		{"foo_bar_2", "fooBar2"},
		{"foo2_bar", "foo2Bar"},
		{"alreadyCamel", "alreadyCamel"},
	}
	for _, c := range jsonCases {
		ck(jsonName(c.in) == c.want, "jsonName "+c.in+" = "+jsonName(c.in))
	}
	sym := newStringTable(4)
	symbols := []string{
		"example.User", "id", "Id", "user_id", "userId", "UserID",
		"ALPHA", "BETA", "example/package/path", "example.User",
		"user_id", "userId",
	}
	uniq := make(map[string]int)
	canonicalIDs := make([]uint32, len(symbols))
	for i, s := range symbols {
		id, err := sym.Intern(s)
		if err != nil {
			ck(false, "sym intern")
		}
		canonicalIDs[i] = id
		uniq[s] = int(id)
	}
	ck(sym.Len() == len(uniq), "symbol table unique count")
	for s, want := range uniq {
		if id, ok := sym.Lookup(s); !ok || int(id) != want {
			ck(false, "symbol lookup "+s)
		}
	}
	// 按相同顺序重建，ID 完全一致。
	sym2 := newStringTable(4)
	for i, s := range symbols {
		id, _ := sym2.Intern(s)
		ck(id == canonicalIDs[i], "rebuild canonical id")
	}
	// 随机顺序重建：ID 顺序不同，但每个字符串仍映射到唯一 ID 且 0..Len-1 连续。
	r := rand.New(rand.NewSource(seed))
	shuffled := append([]string(nil), symbols...)
	r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	sym3 := newStringTable(4)
	shufIDs := make(map[string]uint32)
	for _, s := range shuffled {
		id, err := sym3.Intern(s)
		ckSeed(err == nil, "shuffled intern")
		if prev, ok := shufIDs[s]; ok && prev != id {
			ckSeed(false, "shuffled duplicate id mismatch")
		}
		shufIDs[s] = id
	}
	ckSeed(sym3.Len() == len(uniq), "shuffled unique count")
	used := make(map[uint32]struct{}, sym3.Len())
	for s := range uniq {
		id, ok := shufIDs[s]
		ckSeed(ok, "shuffled lookup")
		used[id] = struct{}{}
		if got, ok := sym3.ByID(id); !ok || got != s {
			ckSeed(false, "shuffled byID")
		}
	}
	ckSeed(len(used) == sym3.Len(), "shuffled ids dense")

	// 7. GC：丢弃原始名字切片，仅保留表/快照，验证内容与 ID。
	longView, _ := bt.ByID(rawIDs[6])
	bigSnap := big.Snapshot()
	big = nil
	raw = nil
	gcStress()
	ck(len(bigSnap) == n && bigSnap[0] == "sym_0000_payload" && bigSnap[n-1] == fmt.Sprintf("sym_%04d_payload", n-1), "snapshot survives GC")
	ck(len(longView) == 65536 && longView == strings.Repeat("x", 65536), "long value survives GC")
	if id, ok := sym3.Lookup("userId"); !ok || id > 32 {
		ck(false, "table query after GC")
	}

	// 8. 随机插入/重复插入：ID 稳定性与冻结后的行为。
	const rn = 500
	randomStrings := make([]string, rn)
	for i := range randomStrings {
		randomStrings[i] = "r_" + strconv.Itoa(i) + "_" + strconv.Itoa(r.Intn(1000))
	}
	rt := newStringTable(4)
	firstIDs := make(map[string]uint32)
	order := make([]string, 0, rn*2)
	for i := 0; i < rn; i++ {
		order = append(order, randomStrings[i])
		if i%2 == 0 {
			order = append(order, randomStrings[r.Intn(rn)])
		}
	}
	r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	for _, s := range order {
		id, err := rt.Intern(s)
		ckSeed(err == nil, "random intern")
		if prev, ok := firstIDs[s]; ok {
			ckSeed(prev == id, "random duplicate id")
		} else {
			firstIDs[s] = id
		}
		if i := int(id); i >= rt.Len() {
			ckSeed(false, "random id range")
		}
	}
	ckSeed(rt.Len() == len(firstIDs), "random unique count")
	for s, id := range firstIDs {
		got, ok := rt.Lookup(s)
		ckSeed(ok && got == id, "random lookup")
		if b, ok := rt.ByID(id); !ok || b != s {
			ckSeed(false, "random byID")
		}
	}
	rt.Freeze()
	for i := 0; i < 50; i++ {
		newS := "new_" + strconv.Itoa(i)
		if _, ok := firstIDs[newS]; ok {
			continue
		}
		if _, err := rt.Intern(newS); err != errFrozen {
			ckSeed(false, "random frozen error")
		}
	}
	gcStress()
	for s, id := range firstIDs {
		got, ok := rt.Lookup(s)
		ckSeed(ok && got == id, "random lookup after GC")
	}

	finish("l10_string_table")
}
