// L13: 大 key 哈希：1KB..64KB 二进制 key 的 map 哈希/等值路径、哈希索引与跨 GC 所有权。
package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type bigEntry struct {
	id   int
	h    uint64
	size int
	sum  byte
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

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 20 {
		n = 20
	}
	return n
}

func fnv1a(p []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, b := range p {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

var keySizes = [...]int{1024, 4096, 8192, 16384, 32768, 65536}

// buildBigKey 生成确定性的超大二进制 key：id 与 gen 在前 14 字节，
// 其余字节来自固定模式；id%5==0 的 key 共享长公共前缀，仅在尾部不同。
func buildBigKey(id, gen int, seed int64, buf []byte) []byte {
	n := keySizes[id%len(keySizes)]
	if cap(buf) < n {
		buf = make([]byte, n)
	}
	buf = buf[:n]
	copy(buf, "BIGKEY")
	binary.BigEndian.PutUint32(buf[6:10], uint32(id))
	binary.BigEndian.PutUint32(buf[10:14], uint32(gen))
	if id%5 == 0 {
		// 长公共前缀：所有这类 key 前 n-16 字节相同。
		for i := 14; i < n-16; i++ {
			buf[i] = byte((i*31 + 7) ^ int(seed&0xff))
		}
	} else {
		for i := 14; i < n; i++ {
			buf[i] = byte((i*17 + id*29 + gen*43) ^ int(seed&0xff))
		}
	}
	suffix := n - 16
	binary.BigEndian.PutUint64(buf[suffix:suffix+8], uint64(id))
	binary.BigEndian.PutUint64(buf[suffix+8:], uint64(gen)*0x9E3779B97F4A7C15)
	return buf
}

func keySum(p []byte) byte {
	var s byte
	for _, b := range p {
		s = s*131 + b
	}
	return s
}

func validEntry(e *bigEntry, id int, key []byte) bool {
	if e == nil || e.id != id || e.size != len(key) || e.h != fnv1a(key) {
		return false
	}
	return e.sum == keySum(key)
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(2)

	for round := 0; round < rounds; round++ {
		r := rand.New(rand.NewSource(curSeed + int64(round)*10007))
		const nKeys = 120
		m := make(map[string]*bigEntry, nKeys)
		adj := make(map[uint64][]int, nKeys)
		buf := make([]byte, 0, 65536)

		// 1) 插入 + 哈希索引；string(buf) 拷贝后立即覆盖源缓冲。
		for id := 0; id < nKeys; id++ {
			buf = buildBigKey(id, 1, curSeed+int64(round), buf)
			k := string(buf)
			h := fnv1a(buf)
			m[k] = &bigEntry{id: id, h: h, size: len(k), sum: keySum(buf)}
			adj[h] = append(adj[h], id)
			for i := range buf {
				buf[i] = 0xEE
			}
			if id%16 == 15 {
				gcStress()
			}
		}
		ck(len(m) == nKeys, "bigkey: map size after insert")
		ck(len(adj) > 0, "bigkey: empty hash index")

		// 2) 用全新缓冲重建同内容 key 查询；哈希桶归属正确（随机探测序）。
		for _, id := range r.Perm(nKeys) {
			buf = buildBigKey(id, 1, curSeed+int64(round), buf)
			k := string(buf)
			e, ok := m[k]
			ckSeed(ok && validEntry(e, id, buf), fmt.Sprintf("bigkey: lookup miss/corrupt id=%d", id))
			h := fnv1a(buf)
			found := false
			for _, bid := range adj[h] {
				if bid == id {
					found = true
					break
				}
			}
			ckSeed(found, fmt.Sprintf("bigkey: hash index missing id=%d", id))
		}

		// 3) 更新每 5 个：同 key 替换 value，条目数不变。
		for id := 0; id < nKeys; id += 5 {
			buf = buildBigKey(id, 1, curSeed+int64(round), buf)
			m[string(buf)] = &bigEntry{id: id, h: fnv1a(buf), size: len(buf), sum: keySum(buf)}
		}
		ck(len(m) == nKeys, "bigkey: size after update")
		gcStress()
		for id := 0; id < nKeys; id += 5 {
			buf = buildBigKey(id, 1, curSeed+int64(round), buf)
			e, ok := m[string(buf)]
			ckSeed(ok && e != nil && e.id == id && e.size == len(buf) && e.h == fnv1a(buf) && e.sum == keySum(buf),
				fmt.Sprintf("bigkey: updated entry bad id=%d", id))
		}

		// 4) 删除每 7 个，GC 后确认缺席，再重插。
		del := make(map[int]bool, nKeys/7+1)
		for id := 0; id < nKeys; id += 7 {
			buf = buildBigKey(id, 1, curSeed+int64(round), buf)
			delete(m, string(buf))
			del[id] = true
		}
		want := nKeys - len(del)
		ck(len(m) == want, "bigkey: size after delete")
		gcStress()
		for id := 0; id < nKeys; id++ {
			buf = buildBigKey(id, 1, curSeed+int64(round), buf)
			e, ok := m[string(buf)]
			if del[id] {
				ckSeed(!ok, fmt.Sprintf("bigkey: deleted id resurrected id=%d", id))
			} else {
				ckSeed(ok && validEntry(e, id, buf), fmt.Sprintf("bigkey: retained entry bad id=%d", id))
			}
		}
		for id := range del {
			buf = buildBigKey(id, 1, curSeed+int64(round), buf)
			m[string(buf)] = &bigEntry{id: id, h: fnv1a(buf), size: len(buf), sum: keySum(buf)}
			delete(del, id)
		}
		ck(len(m) == nKeys, "bigkey: size after reinsert")
		gcStress()

		// 5) 长公共前缀、仅尾字节不同的两个 64KB key：哈希与等值不得串。
		prefix := make([]byte, 64*1024)
		for i := range prefix {
			prefix[i] = byte(i*13 + int(curSeed))
		}
		ka := string(append(append([]byte(nil), prefix...), 'A'))
		kb := string(append(append([]byte(nil), prefix...), 'B'))
		ckSeed(ka != kb, "bigkey: long-prefix keys unexpectedly equal")
		ckSeed(fnv1a([]byte(ka)) != fnv1a([]byte(kb)), "bigkey: long-prefix keys hash collision")
		pm := map[string]int{ka: 1, kb: 2}
		gcStress()
		ck(pm[ka] == 1 && pm[kb] == 2, "bigkey: long-prefix lookup crossed")
		delete(pm, ka)
		ck(pm[kb] == 2, "bigkey: sibling key not independent after delete")
		_, ok := pm[ka]
		ck(!ok, "bigkey: deleted long-prefix key still present")

		// 6) 最终全量校验（按 id 排序，不依赖 map 遍历顺序）。
		ids := make([]int, 0, nKeys)
		for id := 0; id < nKeys; id++ {
			ids = append(ids, id)
		}
		for _, id := range ids {
			buf = buildBigKey(id, 1, curSeed+int64(round), buf)
			e, ok := m[string(buf)]
			ckSeed(ok && validEntry(e, id, buf), fmt.Sprintf("bigkey: final entry bad id=%d", id))
		}
		gcStress()

		if intra := atomic.LoadInt32(&failed); intra != 0 {
			break
		}
		runtime.KeepAlive(m)
		runtime.KeepAlive(adj)
		runtime.KeepAlive(prefix)
		runtime.KeepAlive(pm)
	}

	finish("l13_bigkey_map_hash")
}
