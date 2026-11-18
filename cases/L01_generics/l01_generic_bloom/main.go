// L01: 泛型布隆过滤器：双哈希位图、克隆/并集、字符串与结构体 key 实例。
// 对应 badger y.Filter 的固定位串/无假阴性/位长上限语义。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type bloomPayload struct {
	id   int64
	name string
	blob []byte
}

type structKey struct {
	a int64
	b uint64
}

func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

func fnv64(s string) uint64 {
	const off = 14695981039346656037
	const prime = 1099511628211
	h := uint64(off)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func hashInt64(v int64) uint64 { return mix64(uint64(v) * 0x9e3779b97f4a7c15) }

func hashString(v string) uint64 { return mix64(fnv64(v) ^ 0xa0761d6478bd642f) }

func hashStruct(v structKey) uint64 {
	return mix64(uint64(v.a)*0x9e3779b97f4a7c15 ^ mix64(v.b^0xe7037ed1a0b428db))
}

type bloom[T comparable] struct {
	bits []uint64
	m    uint64
	k    int
	hash func(T) uint64
}

func newBloom[T comparable](n, bitsPerKey, k int, h func(T) uint64) *bloom[T] {
	if n < 1 {
		n = 1
	}
	if bitsPerKey < 1 {
		bitsPerKey = 1
	}
	if k < 1 {
		k = 1
	}
	bits := n * bitsPerKey
	if bits < 64 {
		bits = 64
	}
	words := (bits + 63) / 64
	return &bloom[T]{
		bits: make([]uint64, words),
		m:    uint64(words * 64),
		k:    k,
		hash: h,
	}
}

func (b *bloom[T]) positions(v T) (uint64, uint64) {
	h1 := b.hash(v)
	h2 := mix64(h1>>32|1) | 1
	return h1, h2
}

func (b *bloom[T]) add(v T) {
	h1, h2 := b.positions(v)
	for i := 0; i < b.k; i++ {
		pos := (h1 + uint64(i)*h2) % b.m
		b.bits[pos>>6] |= 1 << (pos & 63)
	}
}

func (b *bloom[T]) has(v T) bool {
	h1, h2 := b.positions(v)
	for i := 0; i < b.k; i++ {
		pos := (h1 + uint64(i)*h2) % b.m
		if b.bits[pos>>6]&(1<<(pos&63)) == 0 {
			return false
		}
	}
	return true
}

func (b *bloom[T]) clone() *bloom[T] {
	c := &bloom[T]{
		bits: append([]uint64(nil), b.bits...),
		m:    b.m,
		k:    b.k,
		hash: b.hash,
	}
	return c
}

func (b *bloom[T]) union(o *bloom[T]) (*bloom[T], error) {
	if b.m != o.m || b.k != o.k {
		return nil, fmt.Errorf("shape mismatch m=%d/%d k=%d/%d", b.m, o.m, b.k, o.k)
	}
	u := b.clone()
	for i := range u.bits {
		u.bits[i] |= o.bits[i]
	}
	return u, nil
}

func (b *bloom[T]) setBits() int {
	n := 0
	for _, w := range b.bits {
		for w != 0 {
			w &= w - 1
			n++
		}
	}
	return n
}

var (
	failed      int32
	gcSink      [][]byte
	payloadSink []*bloomPayload
	uafSeed     int64 = 1
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

func envSeed() int64 {
	n, err := strconv.ParseInt(os.Getenv("UAF_SEED"), 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()
	n := 3000 + int(uafSeed%500)

	// ---- int64：全部已插入 key 不得假阴性；未插入 key 假阳性率受限 ----
	f := newBloom[int64](n, 10, 7, hashInt64)
	for i := 0; i < n; i++ {
		f.add(int64(i))
	}
	gcStress()
	for i := 0; i < n; i++ {
		ck(f.has(int64(i)), fmt.Sprintf("int bloom false negative i=%d", i))
	}
	fp := 0
	for i := n; i < 2*n; i++ {
		if f.has(int64(i)) {
			fp++
		}
	}
	ck(fp*20 <= n, fmt.Sprintf("int bloom false positive too high got=%d/%d", fp, n))
	ck(f.setBits() > 0 && f.setBits() <= n*7, fmt.Sprintf("int bloom bit count out of range got=%d", f.setBits()))

	// 克隆与序列化语义：位图拷贝后结果必须与原过滤器一致。
	clone := f.clone()
	for i := 0; i < 2*n; i++ {
		ck(clone.has(int64(i)) == f.has(int64(i)), fmt.Sprintf("bloom clone divergence i=%d", i))
	}

	// 并集：两个不相交集合的过滤器合并后，两边 key 都不得假阴性。
	// 每边用 20 bit/key，并集后等效 10 bit/key，假阳性率仍有上界。
	fa := newBloom[int64](n, 20, 7, hashInt64)
	fb := newBloom[int64](n, 20, 7, hashInt64)
	for i := 0; i < n; i++ {
		fa.add(int64(i))
	}
	for i := 2 * n; i < 3*n; i++ {
		fb.add(int64(i))
	}
	u, err := fa.union(fb)
	ck(err == nil, fmt.Sprintf("bloom union unexpected error: %v", err))
	if err == nil {
		for i := 0; i < n; i++ {
			ck(u.has(int64(i)), fmt.Sprintf("union lost first-set key i=%d", i))
		}
		for i := 2 * n; i < 3*n; i++ {
			ck(u.has(int64(i)), fmt.Sprintf("union lost second-set key i=%d", i))
		}
		fp = 0
		for i := 3 * n; i < 4*n; i++ {
			if u.has(int64(i)) {
				fp++
			}
		}
		ck(fp*20 <= n, fmt.Sprintf("union false positive too high got=%d/%d", fp, n))
	}

	// 形状不一致的并集必须报错，而不是越界/静默错配。
	bad := newBloom[int64](n+64, 10, 7, hashInt64)
	if _, err := f.union(bad); err == nil {
		ck(false, "bloom union accepted mismatched shape")
	}

	// ---- string key：记录指针跨 GC 保持身份，过滤器仍无假阴性 ----
	sn := 1500 + int(uafSeed%300)
	sf := newBloom[string](sn, 10, 7, hashString)
	records := make(map[string]*bloomPayload, sn)
	for i := 0; i < sn; i++ {
		k := fmt.Sprintf("svc-%05d-%d", i, i%13)
		p := &bloomPayload{
			id:   int64(i),
			name: k,
			blob: []byte{byte(i), byte(i >> 3), byte(i >> 6)},
		}
		records[k] = p
		payloadSink = append(payloadSink, p)
		sf.add(k)
	}
	gcStress()
	for k, p := range records {
		ck(sf.has(k), "string bloom false negative for "+k)
		ck(p.blob[0] == byte(p.id) && p.name == k, "string bloom payload corrupted after GC")
	}
	fp = 0
	for i := 0; i < sn; i++ {
		if sf.has(fmt.Sprintf("miss-%05d-%d", i, i%11)) {
			fp++
		}
	}
	ck(fp*10 <= sn, fmt.Sprintf("string bloom false positive too high got=%d/%d", fp, sn))

	// ---- 结构体 key：验证泛型实例化覆盖复合可比较类型 ----
	var keys []structKey
	bf := newBloom[structKey](700, 10, 7, hashStruct)
	for i := 0; i < 700; i++ {
		k := structKey{a: int64(i) * 31, b: uint64(i) * 0x9e3779b97f4a7c15}
		keys = append(keys, k)
		bf.add(k)
	}
	gcStress()
	for _, k := range keys {
		ck(bf.has(k), fmt.Sprintf("struct bloom false negative a=%d", k.a))
	}

	if atomic.LoadInt32(&failed) == 0 {
		// 保持至少一次对 key 切片的 GC 后使用，防止实现把 filter 状态错误地
		// 依赖在会被回收的对象上。
		gcStress()
		for i := 0; i < len(keys); i += 17 {
			ck(bf.has(keys[i]), fmt.Sprintf("struct bloom lost key after second GC a=%d", keys[i].a))
		}
	}

	finish("l01_generic_bloom")
}
