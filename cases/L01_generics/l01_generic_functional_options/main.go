// L01: 泛型 functional options：默认值回落、后写覆盖、nil 选项、选项链与跨类型实例化。
// 对应 pion/webrtc NewAPI(WithSettingEngine/WithMediaEngine(nil)) 的 Option 语义。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type optHook struct {
	name string
	blob []byte
}

type serverConfig struct {
	addr     string
	maxConns int
	retries  int
	tls      bool
	tags     []string
	hook     *optHook
}

type poolConfig struct {
	name   string
	size   int
	idle   int64
	labels []string
}

// Option[T] 对任意配置类型复用，同一套 combinator 可作用于不同 T。
type Option[T any] func(*T)

func applyOptions[T any](base T, opts ...Option[T]) T {
	for _, o := range opts {
		if o != nil {
			o(&base)
		}
	}
	return base
}

func chainOptions[T any](opts ...Option[T]) Option[T] {
	return func(dst *T) {
		for _, o := range opts {
			if o != nil {
				o(dst)
			}
		}
	}
}

func withAddr(a string) Option[serverConfig] {
	return func(c *serverConfig) { c.addr = a }
}

func withMaxConns(n int) Option[serverConfig] {
	return func(c *serverConfig) { c.maxConns = n }
}

func withRetries(n int) Option[serverConfig] {
	return func(c *serverConfig) { c.retries = n }
}

func withTLS(on bool) Option[serverConfig] {
	return func(c *serverConfig) { c.tls = on }
}

func withTag(tag string) Option[serverConfig] {
	return func(c *serverConfig) { c.tags = append(c.tags, tag) }
}

func withHook(h *optHook) Option[serverConfig] {
	return func(c *serverConfig) { c.hook = h }
}

// withServerDefaults 模拟 webrtc NewAPI 对 nil 注入的回退：只填零值。
func withServerDefaults() Option[serverConfig] {
	return func(c *serverConfig) {
		if c.addr == "" {
			c.addr = "127.0.0.1:0"
		}
		if c.maxConns == 0 {
			c.maxConns = 8
		}
		if c.retries == 0 {
			c.retries = 2
		}
		if len(c.tags) == 0 {
			c.tags = []string{"default"}
		}
	}
}

func withPoolName(name string) Option[poolConfig] {
	return func(c *poolConfig) { c.name = name }
}

func withPoolSize(n int) Option[poolConfig] {
	return func(c *poolConfig) { c.size = n }
}

func withPoolLabel(l string) Option[poolConfig] {
	return func(c *poolConfig) { c.labels = append(c.labels, l) }
}

func validateServer(c serverConfig) []string {
	var errs []string
	if c.addr == "" {
		errs = append(errs, "addr")
	}
	if c.maxConns < 0 {
		errs = append(errs, "maxConns")
	}
	if c.retries < 0 {
		errs = append(errs, "retries")
	}
	seen := make(map[string]bool)
	for _, t := range c.tags {
		if seen[t] {
			errs = append(errs, "dupTag:"+t)
		}
		seen[t] = true
	}
	return errs
}

var (
	failed      int32
	gcSink      [][]byte
	hookSink    []*optHook
	uafSeed     int64 = 1
	addrChoices       = []string{"10.0.0.1:80", "10.0.0.2:443", "127.0.0.1:9000", "[::1]:8080"}
	tagChoices        = []string{"blue", "green", "fast", "safe", "v2"}
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

type rng64 struct{ s uint64 }

func newRNG(seed int64) *rng64 {
	s := uint64(seed)*0x9e3779b97f4a7c15 + 0x51ed
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

func specOption(kind, arg int, hooks []*optHook) Option[serverConfig] {
	switch kind {
	case 0:
		return withAddr(addrChoices[arg%len(addrChoices)])
	case 1:
		return withMaxConns(arg%32 - 2)
	case 2:
		return withRetries(arg%6 - 1)
	case 3:
		return withTLS(arg%2 == 0)
	case 4:
		return withTag(tagChoices[arg%len(tagChoices)])
	case 5:
		return withHook(hooks[arg%len(hooks)])
	case 6:
		return withServerDefaults()
	case 7:
		return nil
	default:
		return chainOptions(
			withAddr(addrChoices[(arg+1)%len(addrChoices)]),
			withRetries((arg%5)+1),
		)
	}
}

// applySpec 是独立于 Option 闭包的参考实现，逐 kind 镜像语义。
func applySpec(e *serverConfig, kind, arg int, hooks []*optHook) {
	switch kind {
	case 0:
		e.addr = addrChoices[arg%len(addrChoices)]
	case 1:
		e.maxConns = arg%32 - 2
	case 2:
		e.retries = arg%6 - 1
	case 3:
		e.tls = arg%2 == 0
	case 4:
		e.tags = append(e.tags, tagChoices[arg%len(tagChoices)])
	case 5:
		e.hook = hooks[arg%len(hooks)]
	case 6:
		if e.addr == "" {
			e.addr = "127.0.0.1:0"
		}
		if e.maxConns == 0 {
			e.maxConns = 8
		}
		if e.retries == 0 {
			e.retries = 2
		}
		if len(e.tags) == 0 {
			e.tags = []string{"default"}
		}
	case 7:
		// nil option must be ignored
	default:
		e.addr = addrChoices[(arg+1)%len(addrChoices)]
		e.retries = (arg % 5) + 1
	}
}

func sameServer(a, b *serverConfig) bool {
	if a.addr != b.addr || a.maxConns != b.maxConns || a.retries != b.retries || a.tls != b.tls {
		return false
	}
	if strings.Join(a.tags, ",") != strings.Join(b.tags, ",") {
		return false
	}
	return a.hook == b.hook
}

func runRandom(seed int64) {
	r := newRNG(seed)
	hooks := make([]*optHook, 4)
	for i := range hooks {
		hooks[i] = &optHook{name: fmt.Sprintf("hook-%d", i), blob: []byte{byte(i), byte(seed)}}
		hookSink = append(hookSink, hooks[i])
	}

	n := 18 + int(seed%9)
	opts := make([]Option[serverConfig], 0, n)
	want := &serverConfig{}
	for i := 0; i < n; i++ {
		kind := r.n(9)
		arg := r.n(64)
		opts = append(opts, specOption(kind, arg, hooks))
		applySpec(want, kind, arg, hooks)
	}

	gcStress()
	got := applyOptions(serverConfig{}, opts...)
	gcStress()
	ck(sameServer(&got, want), fmt.Sprintf("random option result mismatch seed=%d", seed))
	ck(strings.Join(got.tags, ",") == strings.Join(want.tags, ","), "tag order/content mismatch")
	if want.hook != nil {
		ck(got.hook == want.hook, "hook pointer mismatch")
		if got.hook != nil {
			ck(got.hook.name == want.hook.name && got.hook.blob[0] == want.hook.blob[0], "hook payload corrupted after GC")
		}
	}
}

func runFixed() {
	// nil 选项、默认值、后写覆盖。
	got := applyOptions(serverConfig{}, nil, withAddr("a:1"), nil, withAddr("b:2"), withMaxConns(5))
	ck(got.addr == "b:2" && got.maxConns == 5, "later option did not win")
	ck(len(got.tags) == 0, "nil option had side effects")

	// 默认值只填零值，不覆盖显式设置。
	got = applyOptions(serverConfig{}, withMaxConns(3), withServerDefaults())
	ck(got.maxConns == 3 && got.addr == "127.0.0.1:0" && got.retries == 2 && len(got.tags) == 1, "server defaults wrong")

	// 与 webrtc 的 WithMediaEngine(nil) 对应：nil 注入后必须回落到默认对象。
	got = applyOptions(serverConfig{}, withHook(nil), withServerDefaults())
	ck(got.hook == nil, "nil hook should stay nil until explicitly defaultized")

	// 选项链与 nil 组合子。
	chained := chainOptions(withTag("a"), nil, withTag("b"), withRetries(7))
	got = applyOptions(serverConfig{}, chained)
	ck(strings.Join(got.tags, ",") == "a,b" && got.retries == 7, "chained options wrong")

	// 泛型 Option 作用于另一种配置类型。
	pool := applyOptions(poolConfig{},
		withPoolName("shared"),
		nil,
		withPoolSize(16),
		withPoolLabel("io"),
		withPoolLabel("cpu"),
	)
	gcStress()
	ck(pool.name == "shared" && pool.size == 16 && strings.Join(pool.labels, ",") == "io,cpu", "pool options wrong")

	// 校验路径：0 个错误、1 个错误、多个错误。
	ck(len(validateServer(serverConfig{addr: "x", maxConns: 1, tags: []string{"a", "b"}})) == 0, "valid config rejected")
	errs := validateServer(serverConfig{addr: "", maxConns: -1, retries: -1, tags: []string{"a", "a"}})
	ck(len(errs) == 4, fmt.Sprintf("validation error count got=%d want=4", len(errs)))
}

func main() {
	uafSeed = envSeed()
	runFixed()
	runRandom(uafSeed)
	finish("l01_generic_functional_options")
}
