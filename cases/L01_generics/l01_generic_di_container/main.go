// L01: 泛型依赖注入容器：按名称注册 Provider[T]、单例/瞬态生命周期、依赖拓扑解析、
// 循环检测与类型不匹配错误；对应 caddy 模块注册表与 kitex 组件注入测试。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type diConfig struct {
	region string
	blob   []byte
}

type diLogger struct {
	prefix string
	cfg    *diConfig
}

type diStore struct {
	id  int64
	log *diLogger
	cfg *diConfig
}

type diApp struct {
	id    int64
	store *diStore
	cfg   *diConfig
}

type diNode struct {
	name string
}

type diProvider[T any] func(*diContainer) (T, error)

type diRegistration struct {
	fn        any
	singleton bool
}

type diContainer struct {
	regs      map[string]diRegistration
	singles   map[string]any
	resolving map[string]bool
	trace     []string
	nextID    int64
}

func newDI() *diContainer {
	return &diContainer{
		regs:      make(map[string]diRegistration),
		singles:   make(map[string]any),
		resolving: make(map[string]bool),
	}
}

func registerDI[T any](c *diContainer, name string, singleton bool, p diProvider[T]) error {
	if _, ok := c.regs[name]; ok {
		return fmt.Errorf("duplicate registration %q", name)
	}
	c.regs[name] = diRegistration{fn: p, singleton: singleton}
	return nil
}

func resolveDI[T any](c *diContainer, name string) (T, error) {
	var zero T
	if v, ok := c.singles[name]; ok {
		tv, ok := v.(T)
		if !ok {
			return zero, fmt.Errorf("singleton type mismatch for %q", name)
		}
		return tv, nil
	}
	reg, ok := c.regs[name]
	if !ok {
		return zero, fmt.Errorf("unknown service %q", name)
	}
	fn, ok := reg.fn.(diProvider[T])
	if !ok {
		return zero, fmt.Errorf("factory type mismatch for %q", name)
	}
	if c.resolving[name] {
		return zero, fmt.Errorf("dependency cycle at %q", name)
	}
	c.resolving[name] = true
	v, err := fn(c)
	delete(c.resolving, name)
	if err != nil {
		return zero, err
	}
	if reg.singleton {
		c.singles[name] = any(v)
	}
	return v, nil
}

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
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
	s := uint64(seed)*0x9e3779b97f4a7c15 + 0x2468
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

// serviceSet 生成一组注册闭包，调用方可以按任意顺序执行它们。
func serviceSet(c *diContainer) []func() error {
	return []func() error{
		func() error {
			return registerDI(c, "config", true, func(cc *diContainer) (*diConfig, error) {
				cc.trace = append(cc.trace, "config")
				return &diConfig{region: "r1", blob: []byte{1, 2, 3, 4, 5}}, nil
			})
		},
		func() error {
			return registerDI(c, "logger", true, func(cc *diContainer) (*diLogger, error) {
				cc.trace = append(cc.trace, "logger")
				cfg, err := resolveDI[*diConfig](cc, "config")
				if err != nil {
					return nil, err
				}
				return &diLogger{prefix: "svc", cfg: cfg}, nil
			})
		},
		func() error {
			return registerDI(c, "store", true, func(cc *diContainer) (*diStore, error) {
				cc.trace = append(cc.trace, "store")
				cfg, err := resolveDI[*diConfig](cc, "config")
				if err != nil {
					return nil, err
				}
				log, err := resolveDI[*diLogger](cc, "logger")
				if err != nil {
					return nil, err
				}
				return &diStore{id: 7, log: log, cfg: cfg}, nil
			})
		},
		func() error {
			return registerDI(c, "app", false, func(cc *diContainer) (*diApp, error) {
				cc.trace = append(cc.trace, "app")
				store, err := resolveDI[*diStore](cc, "store")
				if err != nil {
					return nil, err
				}
				cfg, err := resolveDI[*diConfig](cc, "config")
				if err != nil {
					return nil, err
				}
				cc.nextID++
				return &diApp{id: cc.nextID, store: store, cfg: cfg}, nil
			})
		},
		func() error {
			return registerDI(c, "tags", true, func(cc *diContainer) ([]string, error) {
				cc.trace = append(cc.trace, "tags")
				return []string{"blue", "green"}, nil
			})
		},
	}
}

func runGraph(seed int64) {
	c := newDI()
	regs := serviceSet(c)
	r := newRNG(seed)
	// 打乱注册顺序：依赖解析必须只由拓扑决定，而不是注册顺序。
	for i := len(regs) - 1; i > 0; i-- {
		j := r.n(i + 1)
		regs[i], regs[j] = regs[j], regs[i]
	}
	for i, reg := range regs {
		ck(reg() == nil, fmt.Sprintf("registration %d failed", i))
	}

	gcStress()
	app1, err := resolveDI[*diApp](c, "app")
	ck(err == nil, fmt.Sprintf("resolve app1 failed: %v", err))
	if err != nil {
		return
	}
	wantTrace := []string{"app", "store", "config", "logger"}
	ck(len(c.trace) == len(wantTrace), fmt.Sprintf("init trace got=%v want=%v", c.trace, wantTrace))
	for i := 0; i < len(c.trace) && i < len(wantTrace); i++ {
		ck(c.trace[i] == wantTrace[i], fmt.Sprintf("init order[%d] got=%q want=%q", i, c.trace[i], wantTrace[i]))
	}

	gcStress()
	app2, err := resolveDI[*diApp](c, "app")
	ck(err == nil, "resolve app2 failed")
	if err == nil {
		ck(app1 != app2, "transient app returned same pointer")
		ck(app1.store == app2.store, "transient apps should share singleton store")
		ck(app1.cfg == app2.cfg, "transient apps should share singleton config")
		ck(app2.store.log == app1.store.log, "logger singleton identity changed")
		ck(app2.cfg.blob[0] == 1 && len(app2.cfg.blob) == 5, "config payload corrupted after GC")
	}
	ck(len(c.trace) == 5 && c.trace[4] == "app", fmt.Sprintf("second app resolution trace got=%v", c.trace))

	// 单例多次解析：同一指针且不再执行 provider。
	cfgA, _ := resolveDI[*diConfig](c, "config")
	cfgB, _ := resolveDI[*diConfig](c, "config")
	ck(cfgA == cfgB && cfgA == app1.cfg, "singleton config identity mismatch")
	ck(len(c.trace) == 5, "singleton resolution re-ran provider")

	// 非指针值类型（切片）的单例缓存。
	t1, err1 := resolveDI[[]string](c, "tags")
	t2, err2 := resolveDI[[]string](c, "tags")
	ck(err1 == nil && err2 == nil && len(t1) == 2 && len(t2) == 2 && t1[0] == t2[0], "slice singleton mismatch")

	// 错误路径。
	if _, err := resolveDI[*diConfig](c, "missing"); err == nil {
		ck(false, "missing service resolved")
	}
	if _, err := resolveDI[int](c, "config"); err == nil {
		ck(false, "type mismatch was not reported")
	}
	if err := registerDI[*diConfig](c, "config", true, func(*diContainer) (*diConfig, error) {
		return &diConfig{region: "dup"}, nil
	}); err == nil {
		ck(false, "duplicate registration accepted")
	}
	cfgC, _ := resolveDI[*diConfig](c, "config")
	ck(cfgC == cfgA, "duplicate registration replaced original")

	// 容器隔离：新容器有独立单例。
	c2 := newDI()
	for _, reg := range serviceSet(c2) {
		ck(reg() == nil, "second container registration failed")
	}
	other, err := resolveDI[*diConfig](c2, "config")
	ck(err == nil && other != cfgA, "containers leaked singleton instances")
}

func runCycle() {
	c := newDI()
	ck(registerDI(c, "a", true, func(cc *diContainer) (*diNode, error) {
		cc.trace = append(cc.trace, "a")
		return resolveDI[*diNode](cc, "b")
	}) == nil, "register a failed")
	ck(registerDI(c, "b", true, func(cc *diContainer) (*diNode, error) {
		cc.trace = append(cc.trace, "b")
		return resolveDI[*diNode](cc, "a")
	}) == nil, "register b failed")

	if _, err := resolveDI[*diNode](c, "a"); err == nil {
		ck(false, "dependency cycle was not detected")
	}
	ck(len(c.resolving) == 0, fmt.Sprintf("resolving set not cleaned after cycle: %v", c.resolving))

	// 循环错误后容器必须仍可继续工作。
	ck(registerDI(c, "ok", true, func(cc *diContainer) (*diNode, error) {
		return &diNode{name: "ok"}, nil
	}) == nil, "register ok failed")
	n, err := resolveDI[*diNode](c, "ok")
	ck(err == nil && n != nil && n.name == "ok", "container unusable after cycle error")

	// 瞬态循环同样要检测。
	c3 := newDI()
	ck(registerDI(c3, "x", false, func(cc *diContainer) (*diNode, error) {
		return resolveDI[*diNode](cc, "y")
	}) == nil, "register x failed")
	ck(registerDI(c3, "y", true, func(cc *diContainer) (*diNode, error) {
		return resolveDI[*diNode](cc, "x")
	}) == nil, "register y failed")
	if _, err := resolveDI[*diNode](c3, "x"); err == nil {
		ck(false, "transient cycle was not detected")
	}
	ck(len(c3.resolving) == 0, "resolving set not cleaned after transient cycle")
}

func main() {
	uafSeed = envSeed()
	runGraph(uafSeed)
	runCycle()
	finish("l01_generic_di_container")
}
