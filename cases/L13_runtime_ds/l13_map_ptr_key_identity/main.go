// L13: map[*node] 指针 key 的身份语义：等值不同指针不命中，增删改查与 GC 后指针仍有效。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type pkNode struct {
	id   int64
	name string
	pad  []byte
}

type pkVal struct {
	id   int64
	key  *pkNode
	blob []byte
}

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

func envSeed() int64 {
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
	if err != nil || v <= 0 {
		return def
	}
	return v
}

func newPKNode(id, salt int64) *pkNode {
	n := &pkNode{id: id, name: fmt.Sprintf("kp-%05d-%02d", id, salt%100), pad: make([]byte, 12)}
	for i := range n.pad {
		n.pad[i] = byte(id*31 + int64(i*7) + salt)
	}
	return n
}

func pkNodeValid(n *pkNode, id, salt int64) bool {
	if n == nil || n.id != id || n.name != fmt.Sprintf("kp-%05d-%02d", id, salt%100) || len(n.pad) != 12 {
		return false
	}
	for i := range n.pad {
		if n.pad[i] != byte(id*31+int64(i*7)+salt) {
			return false
		}
	}
	return true
}

func newPKVal(id, salt int64, key *pkNode) *pkVal {
	v := &pkVal{id: id, key: key, blob: make([]byte, 8)}
	for i := range v.blob {
		v.blob[i] = byte(id*13 + int64(i*5) + salt)
	}
	return v
}

func pkValValid(v *pkVal, id, salt int64, key *pkNode) bool {
	if v == nil || v.id != id || v.key != key || len(v.blob) != 8 {
		return false
	}
	for i := range v.blob {
		if v.blob[i] != byte(id*13+int64(i*5)+salt) {
			return false
		}
	}
	return true
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))

	const n = 900
	m := make(map[*pkNode]*pkVal, n)
	keys := make([]*pkNode, 0, n+1000)
	twins := make([]*pkNode, 0, n+1000)
	vals := make([]*pkVal, 0, n+1000)

	for i := 0; i < n; i++ {
		id := int64(i) + curSeed%500
		k := newPKNode(id, curSeed)
		tw := newPKNode(id, curSeed) // 字段等值但地址不同的 twin key
		v := newPKVal(id, curSeed, k)
		m[k] = v
		keys = append(keys, k)
		twins = append(twins, tw)
		vals = append(vals, v)
	}
	ck(len(m) == n, "ptr key: initial len")

	perm := r.Perm(n)
	validate := func(tag string) {
		for _, i := range perm {
			if !pkNodeValid(keys[i], keys[i].id, curSeed) || !pkNodeValid(twins[i], twins[i].id, curSeed) {
				ckSeed(false, fmt.Sprintf("ptr key: node fields corrupted at %d (%s)", i, tag))
				return
			}
			got := m[keys[i]]
			if vals[i] == nil {
				if got != nil {
					ckSeed(false, fmt.Sprintf("ptr key: deleted key resurrected at %d (%s)", i, tag))
					return
				}
				continue
			}
			if !pkValValid(got, vals[i].id, curSeed, keys[i]) {
				ckSeed(false, fmt.Sprintf("ptr key: value mismatch at %d (%s)", i, tag))
				return
			}
			if _, ok := m[twins[i]]; ok {
				ckSeed(false, fmt.Sprintf("ptr key: twin key aliased at %d (%s)", i, tag))
				return
			}
		}
	}
	validate("build")
	gcStress()
	validate("after build GC")

	// 覆盖：同指针 key 上的 value 指针必须被替换。
	for i := 0; i < n; i += 4 {
		nv := newPKVal(int64(i)+500000, curSeed, keys[i])
		m[keys[i]] = nv
		vals[i] = nv
	}
	ck(len(m) == n, "ptr key: len after overwrite")
	validate("overwrite")
	gcStress()
	validate("after overwrite GC")

	deleted := 0
	for i := 0; i < n; i += 3 {
		delete(m, keys[i])
		vals[i] = nil
		deleted++
	}
	ck(len(m) == n-deleted, "ptr key: len after delete")
	validate("delete")
	gcStress()
	validate("after delete GC")

	for i := 0; i < n; i += 3 {
		nv := newPKVal(int64(i)+900000, curSeed, keys[i])
		m[keys[i]] = nv
		vals[i] = nv
	}
	total := n
	for i := n; i < 1900; i++ {
		id := int64(i) + 1000000
		k := newPKNode(id, curSeed)
		tw := newPKNode(id, curSeed)
		v := newPKVal(id, curSeed, k)
		m[k] = v
		keys = append(keys, k)
		twins = append(twins, tw)
		vals = append(vals, v)
		total++
	}
	ck(len(m) == total, "ptr key: len after growth")
	perm = r.Perm(total)
	validate("growth")
	gcStress()
	validate("after growth GC")

	indexOf := make(map[*pkNode]int, total)
	for i, k := range keys {
		indexOf[k] = i
	}
	aggregate := func(tag string) {
		count := 0
		var sum int64
		bad := false
		for k, v := range m {
			count++
			idx, ok := indexOf[k]
			if !ok || vals[idx] == nil || v != vals[idx] || v.key != k || !pkValValid(v, v.id, curSeed, k) {
				bad = true
				break
			}
			sum += v.id % 1000
		}
		var want int64
		for _, v := range vals {
			if v != nil {
				want += v.id % 1000
			}
		}
		ckSeed(!bad, "ptr key: iteration key/value identity ("+tag+")")
		ck(count == total, "ptr key: iteration count ("+tag+")")
		ck(sum == want, "ptr key: iteration aggregate ("+tag+")")
	}
	aggregate("growth")

	rounds := envInt("TORTURE_ROUNDS", 2)
	for round := 0; round < rounds; round++ {
		p := r.Perm(total)
		removed := 0
		for j := 0; j < total; j += 5 {
			idx := p[j]
			if vals[idx] != nil {
				delete(m, keys[idx])
				vals[idx] = nil
				removed++
			}
		}
		ck(len(m) == total-removed, "ptr key: len mid-churn")
		gcStress()
		validate(fmt.Sprintf("churn %d delete", round))
		for j := 0; j < total; j += 5 {
			idx := p[j]
			if vals[idx] == nil {
				nv := newPKVal(int64(idx)+int64(round+1)*77777+700000, curSeed, keys[idx])
				m[keys[idx]] = nv
				vals[idx] = nv
			}
		}
		ck(len(m) == total, "ptr key: len after churn reinsert")
		perm = r.Perm(total)
		validate(fmt.Sprintf("churn %d reinsert", round))
		gcStress()
		validate(fmt.Sprintf("churn %d GC", round))
	}
	aggregate("final")

	runtime.KeepAlive(m)
	runtime.KeepAlive(keys)
	runtime.KeepAlive(twins)
	runtime.KeepAlive(vals)
	finish("l13_map_ptr_key_identity")
}
