// L02: sync.Once 初始化注册表：并发首次使用只初始化一次（含嵌套 Once），
// 64 个 goroutine 两两竞争注册 32 个扩展项（恰好一半成功、一半重复被拒），
// 并发查找基础项/别名并在 GC 后校验。
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	nBase       = 32
	nExt        = 32
	nRegGors    = 64
	nReaders    = 4
	readerLoops = 150
)

var (
	failed  int32
	gcMu    sync.Mutex
	gcSink  [][]byte
	curSeed int64
	errDup  = errors.New("duplicate registration")
	reg     registry
)

type blob struct {
	tag  int64
	name string
}

type codec struct {
	name  string
	id    int
	magic int64
	blob  *blob
}

type registry struct {
	once     sync.Once
	lazyOnce sync.Once
	initRuns int32
	lazyRuns int32
	mu       sync.RWMutex
	entries  map[string]*codec
	aliases  map[string]string
}

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

func tagFor(seed int64, id int) int64 {
	x := uint64(seed)*0x9E3779B97F4A7C15 + uint64(id+1)*0xBF58476D1CE4E5B9
	x ^= x >> 30
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return int64(x >> 1)
}

func newCodec(name string, id int) *codec {
	return &codec{name: name, id: id, magic: int64(id)*13 + 7, blob: &blob{tag: tagFor(curSeed, id), name: "cm"}}
}

func validCodec(c *codec, id int) bool {
	return c != nil && c.name != "" && c.id == id && c.magic == int64(id)*13+7 &&
		c.blob != nil && c.blob.name == "cm" && c.blob.tag == tagFor(curSeed, id)
}

func (r *registry) ensure() {
	r.once.Do(func() {
		atomic.AddInt32(&r.initRuns, 1)
		r.entries = make(map[string]*codec, nBase+nExt+1)
		r.aliases = make(map[string]string, nBase)
		for i := 0; i < nBase; i++ {
			name := fmt.Sprintf("base-%02d", i)
			r.entries[name] = newCodec(name, i)
			r.aliases[fmt.Sprintf("b%02d", i)] = name
		}
		r.lazyOnce.Do(func() {
			atomic.AddInt32(&r.lazyRuns, 1)
			r.entries["lazy-default"] = newCodec("lazy-default", 1000)
		})
	})
}

func (r *registry) register(c *codec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[c.name]; ok {
		return errDup
	}
	r.entries[c.name] = c
	return nil
}

func (r *registry) lookup(name string) (*codec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if target, ok := r.aliases[name]; ok {
		name = target
	}
	c, ok := r.entries[name]
	return c, ok
}

func main() {
	curSeed = envSeed()
	start := make(chan struct{})
	var regWG, readWG sync.WaitGroup
	var success, dups int32

	for i := 0; i < nRegGors; i++ {
		regWG.Add(1)
		go func(id int) {
			defer regWG.Done()
			<-start
			reg.ensure()
			name := fmt.Sprintf("ext-%02d", id%nExt)
			err := reg.register(newCodec(name, 2000+id))
			switch {
			case err == nil:
				atomic.AddInt32(&success, 1)
			case errors.Is(err, errDup):
				atomic.AddInt32(&dups, 1)
			default:
				ckSeed(false, "unexpected registration error")
			}
		}(i)
	}
	for r := 0; r < nReaders; r++ {
		readWG.Add(1)
		go func(id int) {
			defer readWG.Done()
			<-start
			reg.ensure()
			for i := 0; i < readerLoops; i++ {
				k := (id*13 + i*7) % nBase
				want := fmt.Sprintf("base-%02d", k)
				c, ok := reg.lookup(want)
				if !ok || !validCodec(c, k) {
					ckSeed(false, fmt.Sprintf("base lookup failed key=%s", want))
				}
				alias := fmt.Sprintf("b%02d", k)
				ca, ok := reg.lookup(alias)
				if !ok || ca != c {
					ckSeed(false, fmt.Sprintf("alias lookup mismatch key=%s", alias))
				}
				if i%16 == 0 {
					gcStress()
					if !validCodec(c, k) {
						ckSeed(false, "looked-up codec corrupted after GC")
					}
				}
			}
			if _, ok := reg.lookup("missing"); ok {
				ck(false, "missing key unexpectedly found")
			}
			if c, ok := reg.lookup("lazy-default"); !ok || !validCodec(c, 1000) {
				ckSeed(false, "lazy default codec invalid")
			}
		}(r)
	}
	close(start)
	regWG.Wait()
	readWG.Wait()

	ck(atomic.LoadInt32(&reg.initRuns) == 1, "registry initialized more than once")
	ck(atomic.LoadInt32(&reg.lazyRuns) == 1, "nested lazy Once ran more than once")
	ck(atomic.LoadInt32(&success) == nExt,
		fmt.Sprintf("registration successes got=%d want=%d", success, nExt))
	ck(atomic.LoadInt32(&dups) == nRegGors-nExt,
		fmt.Sprintf("duplicate rejections got=%d want=%d", dups, nRegGors-nExt))

	gcStress()
	reg.mu.RLock()
	total := len(reg.entries)
	reg.mu.RUnlock()
	ck(total == nBase+nExt+1, fmt.Sprintf("registry size got=%d want=%d", total, nBase+nExt+1))
	for i := 0; i < nBase; i++ {
		name := fmt.Sprintf("base-%02d", i)
		c, ok := reg.lookup(name)
		if !ok || !validCodec(c, i) {
			ckSeed(false, fmt.Sprintf("final base entry invalid key=%s", name))
		}
	}
	for i := 0; i < nExt; i++ {
		name := fmt.Sprintf("ext-%02d", i)
		c, ok := reg.lookup(name)
		if !ok || c.name != name || !validCodec(c, c.id) || c.id < 2000 {
			ckSeed(false, fmt.Sprintf("final ext entry invalid key=%s", name))
		}
	}
	for i := 0; i < nBase; i++ {
		base, ok1 := reg.lookup(fmt.Sprintf("base-%02d", i))
		alias, ok2 := reg.lookup(fmt.Sprintf("b%02d", i))
		if !ok1 || !ok2 || base != alias {
			ckSeed(false, fmt.Sprintf("final alias mismatch base-%02d", i))
		}
	}

	// Once 在 f panic 后仍视为已执行：第二次 Do 不得再次调用 f。
	var po sync.Once
	var panicRuns, secondRuns int32
	func() {
		defer func() {
			if recover() == nil {
				ck(false, "expected panic from Once body")
			}
		}()
		po.Do(func() {
			atomic.AddInt32(&panicRuns, 1)
			panic("once-panic")
		})
	}()
	po.Do(func() { atomic.AddInt32(&secondRuns, 1) })
	ck(atomic.LoadInt32(&panicRuns) == 1 && atomic.LoadInt32(&secondRuns) == 0,
		"Once panic semantics mismatch")

	fmt.Printf("init_runs=1 lazy_runs=1 base=%d ext=%d dup_rejected=%d entries=%d lookups=%d\n",
		nBase, nExt, dups, total, nReaders*readerLoops)
	finish("l02_once_registry_init")
}
