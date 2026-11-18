// L10: 生成式枚举 map：别名 canonical 去重、正反查表、允许集合、
// 写时复制快照、重建确定性、map 扩容/删除/GC 与随机 churn 对账。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
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

// ---- 生成式枚举 ----

type role int32

const (
	roleUnknown role = 0
	roleUser    role = 1
	roleMember  role = 1 // 别名，与 roleUser 同值
	roleAdmin   role = 2
	roleOwner   role = 3
)

type enumPair struct {
	num  role
	name string
}

var roleDecl = []enumPair{
	{roleUnknown, "ROLE_UNKNOWN"},
	{roleUser, "ROLE_USER"},
	{roleMember, "ROLE_MEMBER"},
	{roleAdmin, "ROLE_ADMIN"},
	{roleOwner, "ROLE_OWNER"},
}

func buildCanonical(decl []enumPair) map[role]string {
	m := make(map[role]string, len(decl))
	for _, p := range decl {
		if _, ok := m[p.num]; !ok {
			m[p.num] = p.name // 首次声明为 canonical
		}
	}
	return m
}

func buildReverse(decl []enumPair) map[string]role {
	m := make(map[string]role, len(decl))
	for _, p := range decl {
		m[p.name] = p.num
	}
	return m
}

func buildAllowed(canonical map[role]string) map[role]struct{} {
	m := make(map[role]struct{}, len(canonical))
	for v := range canonical {
		m[v] = struct{}{}
	}
	return m
}

func sortedRoles(m map[role]string) []role {
	out := make([]role, 0, len(m))
	for v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func cloneRoleNames(m map[role]string) map[role]string {
	out := make(map[role]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneReverse(m map[string]role) map[string]role {
	out := make(map[string]role, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneSet(m map[role]struct{}) map[role]struct{} {
	out := make(map[role]struct{}, len(m))
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

func setUnion(a, b map[role]struct{}) []role {
	u := make(map[role]struct{}, len(a)+len(b))
	for k := range a {
		u[k] = struct{}{}
	}
	for k := range b {
		u[k] = struct{}{}
	}
	out := make([]role, 0, len(u))
	for k := range u {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func setIntersect(a, b map[role]struct{}) []role {
	out := make([]role, 0)
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func setDifference(a, b map[role]struct{}) []role {
	out := make([]role, 0)
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func dumpCanonical(m map[role]string) string {
	vals := sortedRoles(m)
	var b []byte
	for _, v := range vals {
		b = append(b, fmt.Sprintf("%d=%s;", v, m[v])...)
	}
	return string(b)
}

func checksum(m map[role]string) uint64 {
	var h uint64 = 1469598103934665603
	for _, v := range sortedRoles(m) {
		h ^= uint64(v)
		h *= 1099511628211
		for i := 0; i < len(m[v]); i++ {
			h ^= uint64(m[v][i])
			h *= 1099511628211
		}
	}
	return h
}

func main() {
	seed := int64(1)
	if v := os.Getenv("UAF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	curSeed = seed

	canonical := buildCanonical(roleDecl)
	reverse := buildReverse(roleDecl)
	allowed := buildAllowed(canonical)

	// 1. 别名 canonical / 正反查 / 允许集合。
	ck(len(canonical) == 4, "canonical value count")
	ck(len(reverse) == 5, "reverse name count includes alias")
	ck(canonical[roleUser] == "ROLE_USER", "first declaration is canonical")
	ck(reverse["ROLE_MEMBER"] == roleUser && reverse["ROLE_USER"] == roleUser, "alias maps to same value")
	if name, ok := canonical[roleMember]; !ok || name != "ROLE_USER" {
		ck(false, "alias value canonical lookup")
	}
	if _, ok := canonical[role(99)]; ok {
		ck(false, "unknown enum number absent")
	}
	if _, ok := reverse["ROLE_NOPE"]; ok {
		ck(false, "unknown enum name absent")
	}
	ck(len(allowed) == 4, "allowed set deduplicates aliases")
	if _, ok := allowed[roleUser]; !ok {
		ck(false, "allowed contains alias value once")
	}
	vals := sortedRoles(canonical)
	ck(len(vals) == 4 && vals[0] == roleUnknown && vals[1] == roleUser && vals[2] == roleAdmin && vals[3] == roleOwner, "sorted enum values")
	allValues := make([]role, 0, len(allowed))
	for v := range allowed {
		allValues = append(allValues, v)
	}
	sort.Slice(allValues, func(i, j int) bool { return allValues[i] < allValues[j] })
	ck(len(allValues) == 4 && allValues[0] == roleUnknown && allValues[1] == roleUser, "AllValues sorted and deduplicated")
	allValues[0] = role(42)
	if _, ok := allowed[roleUser]; !ok {
		ck(false, "AllValues copy does not mutate set")
	}

	// 2. 写时复制快照。
	nameSnap := cloneRoleNames(canonical)
	revSnap := cloneReverse(reverse)
	setSnap := cloneSet(allowed)
	canonical[role(50)] = "ROLE_SYNTH"
	reverse["ROLE_SYNTH"] = 50
	allowed[role(50)] = struct{}{}
	ck(len(nameSnap) == 4 && nameSnap[roleUser] == "ROLE_USER", "name snapshot isolated from insert")
	ck(len(revSnap) == 5, "reverse snapshot isolated from insert")
	ck(len(setSnap) == 4, "set snapshot isolated from insert")
	if _, ok := nameSnap[role(50)]; ok {
		ck(false, "snapshot must not see later key")
	}
	nameSnap[roleUser] = "MUTATED"
	revSnap["ROLE_ADMIN"] = 99
	ck(canonical[roleUser] == "ROLE_USER", "mutating snapshot does not affect source")
	ck(reverse["ROLE_ADMIN"] == roleAdmin, "mutating reverse snapshot does not affect source")
	delete(canonical, role(50))
	delete(reverse, "ROLE_SYNTH")
	delete(allowed, role(50))

	// 3. 重建确定性：唯一值声明随机打乱后表内容/校验和一致。
	uniqueDecl := []enumPair{{0, "ROLE_UNKNOWN"}, {1, "ROLE_USER"}, {2, "ROLE_ADMIN"}, {3, "ROLE_OWNER"}}
	r := rand.New(rand.NewSource(seed))
	shuffled := append([]enumPair(nil), uniqueDecl...)
	r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	c1 := buildCanonical(uniqueDecl)
	c2 := buildCanonical(shuffled)
	r1 := buildReverse(uniqueDecl)
	r2 := buildReverse(shuffled)
	ck(dumpCanonical(c1) == dumpCanonical(c2), "rebuild canonical dump identical")
	ck(checksum(c1) == checksum(c2), "rebuild checksum identical")
	ck(len(r1) == len(r2), "rebuild reverse size")
	for k, v := range r1 {
		ckSeed(r2[k] == v, "rebuild reverse mapping")
	}

	// 4. 大表扩容/删除/重插：生成风格 map[int32]string 与反向表。
	const n = 4096
	fwd := make(map[int32]string, 8)
	rev := make(map[string]int32, 8)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("E_%04d", i)
		fwd[int32(i)] = name
		rev[name] = int32(i)
	}
	ck(len(fwd) == n && len(rev) == n, "big enum maps size")
	for i := 0; i < n; i++ {
		name, ok := fwd[int32(i)]
		if !ok || name != fmt.Sprintf("E_%04d", i) {
			ck(false, "big forward lookup")
		}
		if v, ok := rev[name]; !ok || v != int32(i) {
			ck(false, "big reverse lookup")
		}
	}
	fwdSnap := cloneInt32Names(fwd)
	for i := 0; i < n; i += 2 {
		delete(fwd, int32(i))
		delete(rev, fmt.Sprintf("E_%04d", i))
	}
	ck(len(fwd) == n/2 && len(rev) == n/2, "big map delete half")
	for i := 0; i < n; i += 2 {
		if _, ok := fwd[int32(i)]; ok {
			ck(false, "deleted enum number still present")
		}
		if _, ok := rev[fmt.Sprintf("E_%04d", i)]; ok {
			ck(false, "deleted enum name still present")
		}
	}
	gcStress()
	for i := 0; i < n; i++ {
		_, ok := fwd[int32(i)]
		if ok != (i%2 == 1) {
			ck(false, "forward membership after GC")
		}
	}
	ck(len(fwdSnap) == n, "snapshot retains deleted entries")
	for i := 0; i < n; i += 2 {
		name := fmt.Sprintf("E_%04d", i)
		fwd[int32(i)] = name
		rev[name] = int32(i)
	}
	ck(len(fwd) == n && len(rev) == n, "big map reinsert")
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("E_%04d", i)
		if fwd[int32(i)] != name || rev[name] != int32(i) {
			ck(false, "reinsert consistency")
		}
	}

	// 5. 集合代数（去重后的枚举值域）。
	extra := map[role]struct{}{roleAdmin: {}, roleOwner: {}, role(4): {}, role(99): {}}
	u := setUnion(allowed, extra)
	inter := setIntersect(allowed, extra)
	diff := setDifference(allowed, extra)
	ck(len(u) == 6 && u[0] == roleUnknown && u[1] == roleUser && u[5] == role(99), "set union sorted")
	ck(len(inter) == 2 && inter[0] == roleAdmin && inter[1] == roleOwner, "set intersection")
	ck(len(diff) == 2 && diff[0] == roleUnknown && diff[1] == roleUser, "set difference")
	ck(len(setUnion(nil, nil)) == 0 && len(setIntersect(nil, extra)) == 0, "nil set operations")
	empty := map[role]struct{}{roleUser: {}}
	empty[roleUser] = struct{}{}
	ck(len(empty) == 1, "alias assignment does not grow set")

	// 6. GC 后初始表、快照与校验和稳定。
	baseSum := checksum(canonical)
	keepNames := cloneRoleNames(canonical)
	keepRev := cloneReverse(reverse)
	keepAllowed := cloneSet(allowed)
	canonical, reverse, allowed = nil, nil, nil
	gcStress()
	ck(dumpCanonical(keepNames) == "0=ROLE_UNKNOWN;1=ROLE_USER;2=ROLE_ADMIN;3=ROLE_OWNER;", "retained canonical survives GC")
	ck(keepRev["ROLE_MEMBER"] == roleUser && len(keepRev) == 5, "retained reverse survives GC")
	ck(len(keepAllowed) == 4, "retained set survives GC")
	ck(baseSum == checksum(keepNames), "checksum stable across GC")

	// 7. 随机 churn 与模型对账。
	const pool = 64
	mfwd := make(map[int32]string, pool)
	mrev := make(map[string]int32, pool)
	for i := 0; i < 500; i++ {
		idx := int32(r.Intn(pool))
		name := fmt.Sprintf("R_%02d", idx)
		if r.Intn(10) < 7 {
			mfwd[idx] = name
			mrev[name] = idx
		} else {
			delete(mfwd, idx)
			delete(mrev, name)
		}
		if i%50 == 49 {
			gcStress()
			ckSeed(len(mfwd) == len(mrev), "random map sizes agree")
			for k, name := range mfwd {
				ckSeed(mrev[name] == k, "random reverse consistency")
			}
			ckSeed(keepNames[roleUser] == "ROLE_USER", "alias canonical stable")
			ckSeed(keepRev["ROLE_MEMBER"] == roleUser, "alias reverse stable")
			ckSeed(len(keepAllowed) == 4, "allowed set stable")
			ckSeed(checksum(keepNames) == baseSum, "checksum stable during churn")
		}
	}
	for i := int32(0); i < pool; i++ {
		name, ok := mfwd[i]
		if ok {
			ckSeed(mrev[name] == i, "final reverse")
		}
	}
	ckSeed(len(mfwd) == len(mrev), "final map sizes")

	finish("l10_enum_map")
}

func cloneInt32Names(m map[int32]string) map[int32]string {
	out := make(map[int32]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
