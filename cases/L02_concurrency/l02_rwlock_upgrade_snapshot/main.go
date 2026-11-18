// L02: 读锁升级写锁（etcd 快照隔离 / caddy RWMutex map 场景）：
// 读侧先 RLock 查缓存，miss 后释放读锁，串行化升级并二次检查，再持写锁发布不可变值；
// 校验每个 key 只构建一次、升级峰值并发为 1、并发写者版本单调。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	upKeys        = 4
	upReaders     = 8
	upReadRounds  = 24
	upWriters     = 2
	upWritesPerW  = 3
	upValueLength = 16
)

type upValue struct {
	key  int
	ver  int64
	tag  string
	data []byte
}

func newUpValue(key int, ver int64, seed int64) *upValue {
	data := make([]byte, upValueLength)
	for i := range data {
		data[i] = byte(seed + int64(key*31+i) + ver*7)
	}
	return &upValue{key: key, ver: ver, tag: fmt.Sprintf("k%d-v%d", key, ver), data: data}
}

func (v *upValue) valid(seed int64) bool {
	if v == nil {
		return false
	}
	if v.tag != fmt.Sprintf("k%d-v%d", v.key, v.ver) {
		return false
	}
	for i := range v.data {
		if v.data[i] != byte(seed+int64(v.key*31+i)+v.ver*7) {
			return false
		}
	}
	return true
}

type upgradeCache struct {
	mu     sync.RWMutex
	edges  map[int]*upValue
	uMu    sync.Mutex // 串行化所有升级路径；构建期间不持有读锁
	builds int
	active int32
	peak   int32
}

func newUpgradeCache() *upgradeCache {
	return &upgradeCache{edges: make(map[int]*upValue)}
}

func (c *upgradeCache) get(key int) *upValue {
	c.mu.RLock()
	v := c.edges[key]
	c.mu.RUnlock()
	return v
}

func (c *upgradeCache) getOrBuild(key int, seed int64) *upValue {
	c.mu.RLock()
	v := c.edges[key]
	c.mu.RUnlock()
	if v != nil {
		return v
	}

	c.uMu.Lock()
	defer c.uMu.Unlock()
	// 升级后的二次检查：可能已被前一个升级者构建。
	c.mu.RLock()
	v = c.edges[key]
	c.mu.RUnlock()
	if v != nil {
		return v
	}

	n := atomic.AddInt32(&c.active, 1)
	for {
		p := atomic.LoadInt32(&c.peak)
		if n <= p || atomic.CompareAndSwapInt32(&c.peak, p, n) {
			break
		}
	}
	v = newUpValue(key, 1, seed)
	runtime.Gosched()
	gcStress()
	if !v.valid(seed) {
		ck(false, fmt.Sprintf("upgraded value corrupted while building key=%d", key))
	}
	c.mu.Lock()
	c.edges[key] = v
	c.mu.Unlock()
	atomic.AddInt32(&c.active, -1)
	c.builds++
	return v
}

func (c *upgradeCache) update(key int, seed int64) {
	c.mu.Lock()
	old := c.edges[key]
	if old == nil {
		c.mu.Unlock()
		return
	}
	nv := newUpValue(key, old.ver+1, seed)
	c.edges[key] = nv
	c.mu.Unlock()
	runtime.Gosched()
	if !nv.valid(seed) {
		ck(false, fmt.Sprintf("updated value corrupted key=%d ver=%d", key, nv.ver))
	}
}

var (
	failed int32
	gcMu   sync.Mutex
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	gcMu.Lock()
	for i := 0; i < 8; i++ {
		b := make([]byte, 2048)
		b[0] = byte(i)
		gcSink = append(gcSink[:0], b)
	}
	gcSink = gcSink[:0]
	gcMu.Unlock()
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

func seedVal() int64 {
	if s := os.Getenv("UAF_SEED"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return int64(n)
		}
	}
	return 1
}

func main() {
	seed := seedVal()
	c := newUpgradeCache()

	// 阶段一：所有读者同时 miss，升级路径必须二次检查并只构建一次。
	var bwg sync.WaitGroup
	for r := 0; r < upReaders; r++ {
		bwg.Add(1)
		go func(r int) {
			defer bwg.Done()
			for k := 0; k < upKeys; k++ {
				key := (k + r) % upKeys
				v := c.getOrBuild(key, seed)
				if !v.valid(seed) {
					ck(false, fmt.Sprintf("reader %d got invalid built value key=%d", r, key))
				}
			}
		}(r)
	}
	bwg.Wait()

	if c.builds != upKeys {
		ck(false, fmt.Sprintf("builds=%d want=%d", c.builds, upKeys))
	}
	if p := atomic.LoadInt32(&c.peak); p != 1 {
		ck(false, fmt.Sprintf("upgrade peak=%d want=1", p))
	}
	for k := 0; k < upKeys; k++ {
		v := c.get(k)
		if !v.valid(seed) {
			ck(false, fmt.Sprintf("missing/invalid entry after build key=%d", k))
		}
		if v.ver != 1 {
			ck(false, fmt.Sprintf("initial version key=%d ver=%d", k, v.ver))
		}
	}

	// 阶段二：并发读者 + 写者替换不可变值，读侧只能看到完整版本。
	start := make(chan struct{})
	var rwg, wwg sync.WaitGroup
	for r := 0; r < upReaders; r++ {
		rwg.Add(1)
		go func(r int) {
			defer rwg.Done()
			<-start
			for i := 0; i < upReadRounds; i++ {
				key := (i + r) % upKeys
				v := c.get(key)
				if !v.valid(seed) {
					ck(false, fmt.Sprintf("reader %d observed torn value key=%d", r, key))
				}
				if v.ver < 1 || v.ver > 1+upWriters*upWritesPerW {
					ck(false, fmt.Sprintf("reader %d observed out-of-range version key=%d ver=%d", r, key, v.ver))
				}
				if i%9 == 0 {
					gcStress()
				}
			}
		}(r)
	}
	for w := 0; w < upWriters; w++ {
		wwg.Add(1)
		go func(w int) {
			defer wwg.Done()
			<-start
			for i := 0; i < upWritesPerW; i++ {
				for k := 0; k < upKeys; k++ {
					c.update((k+w)%upKeys, seed)
				}
				if i%2 == 0 {
					gcStress()
				}
			}
		}(w)
	}
	close(start)
	rwg.Wait()
	wwg.Wait()

	wantVer := int64(1 + upWriters*upWritesPerW)
	for k := 0; k < upKeys; k++ {
		v := c.get(k)
		if !v.valid(seed) {
			ck(false, fmt.Sprintf("final entry invalid key=%d", k))
		}
		if v.ver != wantVer {
			ck(false, fmt.Sprintf("final version key=%d ver=%d want=%d", k, v.ver, wantVer))
		}
	}
	gcStress()
	for k := 0; k < upKeys; k++ {
		v := c.get(k)
		if !v.valid(seed) {
			ck(false, fmt.Sprintf("final entry corrupted by GC key=%d", k))
		}
	}

	fmt.Printf("keys=%d readers=%d builds=%d final_version=%d\n", upKeys, upReaders, c.builds, wantVer)
	finish("l02_rwlock_upgrade_snapshot")
}
