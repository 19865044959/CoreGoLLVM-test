// L13: 强制哈希碰撞的链式 map：桶内冲突、覆盖、删除压缩、重哈希与跨 GC 指针保活。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type payload struct {
	id   int64
	name string
	salt int64
	pad  []byte
}

type cell struct {
	key string
	val *payload
}

type chainTable struct {
	buckets [][]cell
	n       int
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
	if err != nil || v < 1 {
		return def
	}
	if v > 200 {
		return 200
	}
	return v
}

func newPayload(id, salt int64) *payload {
	p := &payload{id: id, name: fmt.Sprintf("p-%06d", id), salt: salt, pad: make([]byte, 20)}
	for i := range p.pad {
		p.pad[i] = byte(id*11 + int64(i*5) + salt)
	}
	return p
}

func validPayload(p *payload, id, salt int64) bool {
	if p == nil || p.id != id || p.salt != salt || p.name != fmt.Sprintf("p-%06d", id) || len(p.pad) != 20 {
		return false
	}
	for i := range p.pad {
		if p.pad[i] != byte(id*11+int64(i*5)+salt) {
			return false
		}
	}
	return true
}

func hashIndex(k string, nb int) int {
	var h uint32 = 2166136261
	for i := 0; i < len(k); i++ {
		h ^= uint32(k[i])
		h *= 16777619
	}
	return int(h % uint32(nb))
}

func newTable(nb int) *chainTable {
	return &chainTable{buckets: make([][]cell, nb)}
}

func (t *chainTable) get(k string) *payload {
	b := t.buckets[hashIndex(k, len(t.buckets))]
	for i := range b {
		if b[i].key == k {
			return b[i].val
		}
	}
	return nil
}

func (t *chainTable) put(k string, v *payload) *payload {
	b := &t.buckets[hashIndex(k, len(t.buckets))]
	for i := range *b {
		if (*b)[i].key == k {
			old := (*b)[i].val
			(*b)[i].val = v
			return old
		}
	}
	*b = append(*b, cell{key: k, val: v})
	t.n++
	return nil
}

func (t *chainTable) del(k string) *payload {
	b := &t.buckets[hashIndex(k, len(t.buckets))]
	for i := range *b {
		if (*b)[i].key == k {
			old := (*b)[i].val
			copy((*b)[i:], (*b)[i+1:])
			(*b)[len(*b)-1] = cell{}
			*b = (*b)[:len(*b)-1]
			t.n--
			return old
		}
	}
	return nil
}

func main() {
	curSeed = envSeed()
	r := rand.New(rand.NewSource(curSeed))
	rounds := envInt("TORTURE_ROUNDS", 2)

	for round := 0; round < rounds; round++ {
		salt := curSeed + int64(round)*7919
		prefix := fmt.Sprintf("r%03d-", round)
		const n = 2400

		t := newTable(8)
		ref := make(map[string]*payload, n)
		keys := make([]string, 0, n)
		for i := 0; i < n; i++ {
			k := prefix + fmt.Sprintf("k%05d", i)
			v := newPayload(int64(i)+int64(round)*100000, salt)
			if old := t.put(k, v); old != nil {
				ckSeed(false, fmt.Sprintf("collision: duplicate insert at %d", i))
				break
			}
			ref[k] = v
			keys = append(keys, k)
		}
		ck(t.n == n && len(ref) == n, "collision: initial count")

		// 8 个桶承载 n 个 key，鸽巢原理保证最大桶至少 n/8，冲突路径必然被走到。
		maxBucket, sumBucket := 0, 0
		for _, b := range t.buckets {
			sumBucket += len(b)
			if len(b) > maxBucket {
				maxBucket = len(b)
			}
		}
		ck(sumBucket == n && maxBucket >= (n+7)/8, "collision: buckets not congested")
		ck(maxBucket > 1, "collision: no chain formed")

		perm := r.Perm(n)
		validate := func(tag string) bool {
			for _, i := range perm {
				k := keys[i]
				want := ref[k]
				got := t.get(string(append([]byte(nil), k...)))
				if got != want {
					ckSeed(false, fmt.Sprintf("collision: %s lookup mismatch at %d", tag, i))
					return false
				}
				if want != nil && !validPayload(want, want.id, want.salt) {
					ckSeed(false, fmt.Sprintf("collision: %s payload corrupt at %d", tag, i))
					return false
				}
			}
			return true
		}
		validate("build")
		gcStress()
		validate("after build GC")

		// 覆盖：同一 key 的 value 指针被替换，长度不变。
		for i := 0; i < n; i += 6 {
			k := keys[i]
			v := newPayload(int64(i)+500000, salt+1)
			if old := t.put(k, v); old != ref[k] {
				ck(false, "collision: overwrite did not return old value")
				break
			}
			ref[k] = v
		}
		ck(t.n == n, "collision: count after overwrite")
		validate("overwrite")
		gcStress()
		validate("after overwrite GC")

		// 删除每 3 个：链内元素前移压缩、尾槽清零。
		removed := 0
		for i := 0; i < n; i += 3 {
			k := keys[i]
			if old := t.del(k); old != ref[k] {
				ck(false, "collision: delete returned wrong value")
				break
			}
			delete(ref, k)
			removed++
		}
		ck(t.n == n-removed && t.n == len(ref), "collision: count after delete")
		validate("delete")
		for i := 0; i < n; i += 3 {
			if t.get(keys[i]) != nil {
				ckSeed(false, fmt.Sprintf("collision: deleted key resurrected at %d", i))
				break
			}
		}
		gcStress()
		validate("after delete GC")

		// 重插被删除的 key，再重哈希到 64 桶。
		for i := 0; i < n; i += 3 {
			k := keys[i]
			v := newPayload(int64(i)+900000, salt+2)
			if old := t.put(k, v); old != nil {
				ck(false, "collision: reinsert saw duplicate")
				break
			}
			ref[k] = v
		}
		ck(t.n == n && len(ref) == n, "collision: count after reinsert")
		validate("reinsert")

		nt := newTable(64)
		for _, b := range t.buckets {
			for _, c := range b {
				nt.put(c.key, c.val)
			}
		}
		ck(nt.n == n, "collision: rehash count")
		old := t
		t = nt
		validate("rehash")
		gcStress()
		validate("after rehash GC")

		var got, want int64
		for _, b := range t.buckets {
			for _, c := range b {
				got += c.val.id % 1000
			}
		}
		for _, v := range ref {
			want += v.id % 1000
		}
		ck(got == want, "collision: aggregate mismatch")
		ck(len(old.buckets) == 8 && len(t.buckets) == 64, "collision: table sizing")
		runtime.KeepAlive(ref)
		runtime.KeepAlive(old)
	}

	finish("l13_hashmap_collision_probe")
}
