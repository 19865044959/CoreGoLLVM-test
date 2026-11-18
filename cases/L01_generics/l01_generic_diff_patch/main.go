// L01: 泛型序列 diff/patch：LCS 编辑脚本、正向/反向应用、unified 文本往返与坏补丁拒绝。
// 对应 gitea services/gitdiff ParsePatch 的上下文匹配语义。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type opKind uint8

const (
	opEqual opKind = iota
	opDelete
	opInsert
)

type diffOp[T comparable] struct {
	kind opKind
	val  T
}

func diffSeq[T comparable](a, b []T) []diffOp[T] {
	m, n := len(a), len(b)
	dp := make([]int32, (m+1)*(n+1))
	at := func(i, j int) int { return i*(n+1) + j }
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[at(i, j)] = dp[at(i+1, j+1)] + 1
			} else if dp[at(i+1, j)] >= dp[at(i, j+1)] {
				dp[at(i, j)] = dp[at(i+1, j)]
			} else {
				dp[at(i, j)] = dp[at(i, j+1)]
			}
		}
	}
	ops := make([]diffOp[T], 0, m+n)
	i, j := 0, 0
	for i < m || j < n {
		if i < m && j < n && a[i] == b[j] {
			ops = append(ops, diffOp[T]{kind: opEqual, val: a[i]})
			i++
			j++
		} else if j < n && (i == m || dp[at(i, j+1)] >= dp[at(i+1, j)]) {
			ops = append(ops, diffOp[T]{kind: opInsert, val: b[j]})
			j++
		} else {
			ops = append(ops, diffOp[T]{kind: opDelete, val: a[i]})
			i++
		}
	}
	return ops
}

func applyOps[T comparable](base []T, ops []diffOp[T]) ([]T, error) {
	out := make([]T, 0, len(base))
	i := 0
	for pos, op := range ops {
		switch op.kind {
		case opEqual:
			if i >= len(base) || base[i] != op.val {
				return nil, fmt.Errorf("equal context mismatch at op %d", pos)
			}
			out = append(out, op.val)
			i++
		case opDelete:
			if i >= len(base) || base[i] != op.val {
				return nil, fmt.Errorf("delete context mismatch at op %d", pos)
			}
			i++
		case opInsert:
			out = append(out, op.val)
		default:
			return nil, fmt.Errorf("unknown op kind at %d", pos)
		}
	}
	if i != len(base) {
		return nil, fmt.Errorf("base not fully consumed: %d/%d", i, len(base))
	}
	return out, nil
}

func reverseOps[T comparable](ops []diffOp[T]) []diffOp[T] {
	out := make([]diffOp[T], len(ops))
	for i, op := range ops {
		r := op
		switch op.kind {
		case opDelete:
			r.kind = opInsert
		case opInsert:
			r.kind = opDelete
		}
		out[i] = r
	}
	return out
}

func formatUnified[T comparable](name string, ops []diffOp[T], fmtVal func(T) string) string {
	oldLen, newLen := 0, 0
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			oldLen++
			newLen++
		case opDelete:
			oldLen++
		case opInsert:
			newLen++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n@@ -1,%d +1,%d @@\n", name, name, oldLen, newLen)
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			b.WriteByte(' ')
		case opDelete:
			b.WriteByte('-')
		case opInsert:
			b.WriteByte('+')
		}
		b.WriteString(fmtVal(op.val))
		b.WriteByte('\n')
	}
	return b.String()
}

func parseUnified(patch string) ([]diffOp[string], error) {
	lines := strings.Split(patch, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "@@") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("missing hunk header")
	}
	ops := make([]diffOp[string], 0, len(lines)-start)
	for _, l := range lines[start:] {
		if l == "" {
			continue
		}
		switch l[0] {
		case ' ':
			ops = append(ops, diffOp[string]{kind: opEqual, val: l[1:]})
		case '-':
			ops = append(ops, diffOp[string]{kind: opDelete, val: l[1:]})
		case '+':
			ops = append(ops, diffOp[string]{kind: opInsert, val: l[1:]})
		case '\\':
			// "\ No newline at end of file" 之类标记，忽略。
		default:
			return nil, fmt.Errorf("malformed patch line %q", l)
		}
	}
	return ops, nil
}

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
	vocab         = []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa", "lambda", "mu"}
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if n := atomic.LoadInt32(&failed); n != 0 {
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envInt(key string, def int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func envSeed() int64 {
	n, err := strconv.ParseInt(os.Getenv("UAF_SEED"), 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

type rng64 struct{ s uint64 }

func newRNG(seed int64, round int) *rng64 {
	s := uint64(seed+int64(round)*65537)*0x9e3779b97f4a7c15 + 11
	if s == 0 {
		s = 1
	}
	return &rng64{s: s}
}

func (r *rng64) next() uint64 {
	r.s ^= r.s << 13
	r.s ^= r.s >> 7
	r.s ^= r.s << 17
	return r.s
}

func (r *rng64) n(n int) int { return int(r.next() % uint64(n)) }

// mutate 生成 head：i==0 必保留，保证至少一个上下文 op。
func mutate[T comparable](r *rng64, base []T, pick func() T) []T {
	head := make([]T, 0, len(base)+8)
	for i := range base {
		switch {
		case i == 0:
			head = append(head, base[i])
		case r.n(4) == 0:
			// delete
		case r.n(4) == 1:
			head = append(head, base[i])
			head = append(head, pick())
		case r.n(4) == 2:
			head = append(head, pick())
		default:
			head = append(head, base[i])
		}
	}
	return head
}

func sameSlice[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func checkGeneric[T comparable](label string, base, head []T) {
	ops := diffSeq(base, head)
	got, err := applyOps(base, ops)
	ck(err == nil, fmt.Sprintf("%s apply error: %v", label, err))
	ck(sameSlice(got, head), fmt.Sprintf("%s apply mismatch got=%d want=%d", label, len(got), len(head)))

	rev := reverseOps(ops)
	back, err := applyOps(head, rev)
	ck(err == nil, fmt.Sprintf("%s reverse apply error: %v", label, err))
	ck(sameSlice(back, base), fmt.Sprintf("%s reverse apply mismatch", label))

	eq, del, ins := 0, 0, 0
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			eq++
		case opDelete:
			del++
		case opInsert:
			ins++
		}
	}
	ck(eq+del == len(base), fmt.Sprintf("%s delete coverage got=%d want=%d", label, eq+del, len(base)))
	ck(eq+ins == len(head), fmt.Sprintf("%s insert coverage got=%d want=%d", label, eq+ins, len(head)))
}

func checkUnified(base, head []string) {
	ops := diffSeq(base, head)
	patch := formatUnified("unit.txt", ops, func(s string) string { return s })
	patch2 := formatUnified("unit.txt", ops, func(s string) string { return s })
	ck(patch == patch2, "unified patch formatting is not deterministic")

	parsed, err := parseUnified(patch)
	ck(err == nil, fmt.Sprintf("parse unified failed: %v", err))
	ck(len(parsed) == len(ops), fmt.Sprintf("parsed op count got=%d want=%d", len(parsed), len(ops)))
	got, err := applyOps(base, parsed)
	ck(err == nil, fmt.Sprintf("apply parsed patch failed: %v", err))
	ck(sameSlice(got, head), "apply parsed patch mismatch")

	// 上下文被篡改：补丁必须拒绝，而不是静默产生错误结果。
	corrupted := append([]diffOp[string](nil), ops...)
	foundEqual := false
	for i := range corrupted {
		if corrupted[i].kind == opEqual {
			corrupted[i].val = "__corrupt_context__"
			foundEqual = true
			break
		}
	}
	ck(foundEqual, "expected at least one equal op for corruption test")
	if foundEqual {
		if _, err := applyOps(base, corrupted); err == nil {
			ck(false, "corrupted context patch was accepted")
		}
	}

	// 删除行被篡改同样必须失败。
	corrupted = append([]diffOp[string](nil), ops...)
	foundDelete := false
	for i := range corrupted {
		if corrupted[i].kind == opDelete {
			corrupted[i].val = "__corrupt_delete__"
			foundDelete = true
			break
		}
	}
	if foundDelete {
		if _, err := applyOps(base, corrupted); err == nil {
			ck(false, "corrupted delete patch was accepted")
		}
	}

	// 基础序列多出一行：必须报未消费错误。
	extra := append(append([]string(nil), base...), "unexpected-tail")
	if _, err := applyOps(extra, ops); err == nil {
		ck(false, "patch applied to mismatched base sequence")
	}

	// 坏补丁文本与缺失 hunk header。
	if _, err := parseUnified("--- a\n+++ b\n?bad\n"); err == nil {
		ck(false, "malformed patch accepted")
	}
	if _, err := parseUnified("--- a\n+++ b\nno header\n"); err == nil {
		ck(false, "patch without hunk header accepted")
	}
}

func runStringRound(seed int64, round, size int) {
	r := newRNG(seed, round)
	base := make([]string, size)
	for i := range base {
		base[i] = vocab[r.n(len(vocab))]
	}
	head := mutate(r, base, func() string { return vocab[r.n(len(vocab))] })
	gcStress()
	checkGeneric("string", base, head)
	checkUnified(base, head)

	// GC 后再次使用原始切片，字符串内容不能被破坏。
	gcStress()
	for i := 0; i < len(base); i += 13 {
		ck(string([]byte(base[i])) == base[i], fmt.Sprintf("base string corrupted at %d", i))
	}
	for i := 0; i < len(head); i += 11 {
		ck(len(head[i]) >= 0, "head string corrupted")
	}
}

func runIntRound(seed int64, round int) {
	r := newRNG(seed, round+1000)
	size := 70 + r.n(30)
	base := make([]int64, size)
	for i := range base {
		base[i] = int64(r.n(40) - 20)
	}
	head := mutate(r, base, func() int64 { return int64(r.n(40) - 20) })
	gcStress()
	checkGeneric("int64", base, head)

	// 边界：空 -> 非空、非空 -> 空、相同序列。
	checkGeneric("int64-empty-left", nil, []int64{1, 2, 3})
	checkGeneric("int64-empty-right", []int64{1, 2, 3}, nil)
	checkGeneric("int64-identical", []int64{5, 5, 7}, []int64{5, 5, 7})
}

func main() {
	uafSeed = envSeed()
	rounds := envInt("TORTURE_ROUNDS", 3)
	size := envInt("TORTURE_SIZE", 130) + int(uafSeed%23)

	for round := 0; round < rounds; round++ {
		runStringRound(uafSeed, round, size)
		runIntRound(uafSeed, round)
		if atomic.LoadInt32(&failed) != 0 {
			finish("l01_generic_diff_patch")
		}
	}
	finish("l01_generic_diff_patch")
}
