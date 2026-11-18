// L10: 生成风格 resolver 表：scheme→builder 注册、target 解析、
// AddressMap/EndpointMap 的等价键语义、状态迁移与 GC 保活。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
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

// ---- Address / Endpoint 模型 ----

type attr struct {
	key string
	val int64
}

func canonAttrs(attrs []attr) string {
	cp := append([]attr(nil), attrs...)
	sort.Slice(cp, func(i, j int) bool {
		if cp[i].key != cp[j].key {
			return cp[i].key < cp[j].key
		}
		return cp[i].val < cp[j].val
	})
	var b strings.Builder
	for _, a := range cp {
		fmt.Fprintf(&b, "%s=%d;", a.key, a.val)
	}
	return b.String()
}

type address struct {
	addr          string
	serverName    string
	attrs         string
	balancerAttrs string
}

func mkAddress(addr, server string, balancer string, attrs ...attr) address {
	return address{addr: addr, serverName: server, attrs: canonAttrs(attrs), balancerAttrs: balancer}
}

// Address 的相等性忽略 BalancerAttributes（与 grpc-go resolver.Address 一致）。
func (a address) coreKey() string {
	return a.addr + "\x00" + a.serverName + "\x00" + a.attrs
}

type addrEntry struct {
	a address
	v any
}

type addressMap struct {
	m map[string]addrEntry
}

func newAddressMap() *addressMap {
	return &addressMap{m: make(map[string]addrEntry)}
}

func (am *addressMap) Set(a address, v any) {
	am.m[a.coreKey()] = addrEntry{a: a, v: v}
}

func (am *addressMap) Get(a address) (any, bool) {
	e, ok := am.m[a.coreKey()]
	if !ok {
		return nil, false
	}
	return e.v, true
}

func (am *addressMap) Delete(a address) {
	delete(am.m, a.coreKey())
}

func (am *addressMap) Len() int { return len(am.m) }

func (am *addressMap) Keys() []address {
	keys := make([]string, 0, len(am.m))
	for k := range am.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]address, 0, len(keys))
	for _, k := range keys {
		out = append(out, am.m[k].a)
	}
	return out
}

func (am *addressMap) Values() []any {
	keys := make([]string, 0, len(am.m))
	for k := range am.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, k := range keys {
		out = append(out, am.m[k].v)
	}
	return out
}

type endpoint struct {
	addresses []address
}

func endpointKey(e endpoint) string {
	set := make(map[string]struct{}, len(e.addresses))
	for _, a := range e.addresses {
		set[a.coreKey()] = struct{}{}
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, "\x00")
}

type endpointEntry struct {
	e endpoint
	v any
}

type endpointMap struct {
	m map[string]endpointEntry
}

func newEndpointMap() *endpointMap {
	return &endpointMap{m: make(map[string]endpointEntry)}
}

func (em *endpointMap) Set(e endpoint, v any) {
	em.m[endpointKey(e)] = endpointEntry{e: e, v: v}
}

func (em *endpointMap) Get(e endpoint) (any, bool) {
	ent, ok := em.m[endpointKey(e)]
	if !ok {
		return nil, false
	}
	return ent.v, true
}

func (em *endpointMap) Delete(e endpoint) { delete(em.m, endpointKey(e)) }

func (em *endpointMap) Len() int { return len(em.m) }

func (em *endpointMap) Keys() []endpoint {
	keys := make([]string, 0, len(em.m))
	for k := range em.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]endpoint, 0, len(keys))
	for _, k := range keys {
		out = append(out, em.m[k].e)
	}
	return out
}

// ---- scheme → builder 注册表 ----

var (
	errDupScheme     = errors.New("resolver: scheme already registered")
	errFrozen        = errors.New("resolver: registry frozen")
	errEmptyScheme   = errors.New("resolver: empty scheme")
	errEmptyEndpoint = errors.New("resolver: empty endpoint")
	errUnknownScheme = errors.New("resolver: unknown scheme")
)

type resolution struct {
	endpoints []endpoint
}

type builder struct {
	scheme string
	build  func(endpoint string) (*resolution, error)
}

type builderRegistry struct {
	m      map[string]*builder
	frozen bool
}

func newBuilderRegistry() *builderRegistry {
	return &builderRegistry{m: make(map[string]*builder)}
}

func (r *builderRegistry) Register(scheme string, fn func(string) (*resolution, error)) error {
	if r.frozen {
		return errFrozen
	}
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	if scheme == "" || fn == nil {
		return errEmptyScheme
	}
	if _, ok := r.m[scheme]; ok {
		return errDupScheme
	}
	r.m[scheme] = &builder{scheme: scheme, build: fn}
	return nil
}

func (r *builderRegistry) Get(scheme string) (*builder, bool) {
	b, ok := r.m[strings.ToLower(strings.TrimSpace(scheme))]
	return b, ok
}

func (r *builderRegistry) Freeze() { r.frozen = true }

func (r *builderRegistry) Names() []string {
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func parseTarget(t string) (scheme, authority, ep string, err error) {
	t = strings.TrimSpace(t)
	if t == "" {
		return "", "", "", errors.New("resolver: empty target")
	}
	scheme = "passthrough"
	rest := t
	if i := strings.Index(t, "://"); i >= 0 {
		scheme = strings.ToLower(t[:i])
		rest = t[i+3:]
		if scheme == "" {
			return "", "", "", errors.New("resolver: empty scheme in target")
		}
	}
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		authority = rest[:j]
		ep = rest[j+1:]
	} else {
		if strings.Contains(t, "://") {
			return "", "", "", errEmptyEndpoint
		}
		ep = rest
	}
	if ep == "" {
		return "", "", "", errEmptyEndpoint
	}
	return scheme, authority, ep, nil
}

func validateResolution(res *resolution) error {
	if res == nil || len(res.endpoints) == 0 {
		return errors.New("resolver: empty endpoints")
	}
	seen := make(map[string]struct{}, len(res.endpoints))
	for _, e := range res.endpoints {
		if len(e.addresses) == 0 {
			return errors.New("resolver: endpoint without addresses")
		}
		for _, a := range e.addresses {
			if a.addr == "" {
				return errors.New("resolver: empty address")
			}
		}
		k := endpointKey(e)
		if _, ok := seen[k]; ok {
			return errors.New("resolver: duplicate endpoint")
		}
		seen[k] = struct{}{}
	}
	return nil
}

func (r *builderRegistry) Resolve(target string) (*resolution, error) {
	scheme, _, ep, err := parseTarget(target)
	if err != nil {
		return nil, err
	}
	b, ok := r.Get(scheme)
	if !ok {
		return nil, fmt.Errorf("%w %q", errUnknownScheme, scheme)
	}
	res, err := b.build(ep)
	if err != nil {
		return nil, err
	}
	if err := validateResolution(res); err != nil {
		return nil, err
	}
	return res, nil
}

// ---- resolver 状态机 ----

type resolverState struct {
	state     string
	endpoints []endpoint
	err       error
}

func newResolverState() *resolverState { return &resolverState{state: "IDLE"} }

func (s *resolverState) Resolve(target string, reg *builderRegistry) {
	s.state = "RESOLVING"
	res, err := reg.Resolve(target)
	if err != nil {
		s.state = "TRANSIENT_FAILURE"
		s.err = err
		s.endpoints = nil
		return
	}
	s.state = "RESOLVED"
	s.err = nil
	s.endpoints = res.endpoints
}

// ---- 测试 ----

func buildRegistry() *builderRegistry {
	reg := newBuilderRegistry()
	must := func(err error, what string) {
		if err != nil {
			ck(false, "register "+what+": "+err.Error())
		}
	}
	must(reg.Register("static", func(ep string) (*resolution, error) {
		parts := strings.Split(ep, ",")
		res := &resolution{}
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				return nil, errors.New("static: empty entry")
			}
			res.endpoints = append(res.endpoints, endpoint{addresses: []address{
				mkAddress(p, "srv", "", attr{"zone", 1}),
			}})
		}
		return res, nil
	}), "static")
	must(reg.Register("Split", func(ep string) (*resolution, error) {
		parts := strings.Split(ep, ",")
		if len(parts) != 2 {
			return nil, errors.New("split: want two entries")
		}
		return &resolution{endpoints: []endpoint{
			{addresses: []address{mkAddress(parts[0], "srv", "", attr{"z", 1}), mkAddress(parts[1], "srv", "", attr{"z", 2})}},
			{addresses: []address{mkAddress(parts[0], "srv", "", attr{"z", 3})}},
		}}, nil
	}), "split")
	must(reg.Register("bad", func(ep string) (*resolution, error) {
		return &resolution{}, nil
	}), "bad")
	return reg
}

func main() {
	seed := int64(1)
	if v := os.Getenv("UAF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	curSeed = seed

	// 1. target 解析。
	cases := []struct {
		target    string
		scheme    string
		authority string
		ep        string
		ok        bool
	}{
		{"dns:///example.com:443", "dns", "", "example.com:443", true},
		{"passthrough:///x", "passthrough", "", "x", true},
		{"static://authority/a:1,b:2", "static", "authority", "a:1,b:2", true},
		{"plain:123", "passthrough", "", "plain:123", true},
		{"DNS:///HOST", "dns", "", "HOST", true},
		{"dns:///a/b", "dns", "", "a/b", true},
		{"", "", "", "", false},
		{"dns://", "", "", "", false},
		{"dns://host", "", "", "", false},
		{"://x", "", "", "", false},
	}
	for _, c := range cases {
		scheme, auth, ep, err := parseTarget(c.target)
		if !c.ok {
			ck(err != nil, "parse should fail: "+c.target)
			continue
		}
		ck(err == nil && scheme == c.scheme && auth == c.authority && ep == c.ep,
			fmt.Sprintf("parse %q = (%q,%q,%q,%v)", c.target, scheme, auth, ep, err))
	}

	// 2. 注册表：大小写、重复、冻结、名字排序。
	reg := buildRegistry()
	names := reg.Names()
	ck(len(names) == 3 && names[0] == "bad" && names[1] == "split" && names[2] == "static", "registry names sorted")
	ck(reg.Register("static", func(string) (*resolution, error) { return nil, nil }) == errDupScheme, "duplicate scheme rejected")
	ck(reg.Register("STATIC", func(string) (*resolution, error) { return nil, nil }) == errDupScheme, "duplicate is case-insensitive")
	if _, ok := reg.Get("SPLIT"); !ok {
		ck(false, "Get is case-insensitive")
	}
	if _, ok := reg.Get("dns"); ok {
		ck(false, "unknown scheme should be absent")
	}
	reg.Freeze()
	ck(reg.Register("late", func(string) (*resolution, error) { return nil, nil }) == errFrozen, "frozen registry rejects register")
	ck(len(reg.Names()) == 3, "frozen registry unchanged")

	// 3. Resolve：endpoint 顺序、authority 忽略、错误路径、状态迁移。
	res, err := reg.Resolve("static://authority/a:1,b:2")
	ck(err == nil && len(res.endpoints) == 2, "static resolve endpoints")
	ck(res != nil && res.endpoints[0].addresses[0].addr == "a:1" && res.endpoints[1].addresses[0].addr == "b:2", "static endpoint order")
	res, err = reg.Resolve("split:///h1:1,h2:2")
	ck(err == nil && len(res.endpoints) == 2, "split resolve endpoints")
	ck(res != nil && len(res.endpoints[0].addresses) == 2 && res.endpoints[0].addresses[0].addr == "h1:1", "split multi-address endpoint")
	_, err = reg.Resolve("dns:///x")
	ck(errors.Is(err, errUnknownScheme), "unknown scheme error")
	_, err = reg.Resolve("bad:///x")
	ck(err != nil, "builder validation error propagated")
	_, err = reg.Resolve("split:///only-one")
	ck(err != nil, "builder argument error propagated")
	_, err = reg.Resolve("static:///a,b,")
	ck(err != nil, "empty endpoint entry rejected")

	st := newResolverState()
	ck(st.state == "IDLE", "initial resolver state")
	st.Resolve("static:///a:1", reg)
	ck(st.state == "RESOLVED" && st.err == nil && len(st.endpoints) == 1, "state RESOLVED")
	st.Resolve("static:///bad,", reg)
	ck(st.state == "TRANSIENT_FAILURE" && st.err != nil && st.endpoints == nil, "state TRANSIENT_FAILURE")
	st.Resolve("static:///b:2", reg)
	ck(st.state == "RESOLVED" && len(st.endpoints) == 1, "recover after failure")

	// 4. AddressMap：BalancerAttributes 别名、覆盖、删除、排序。
	a1 := mkAddress("a1", "s1", "", attr{"a1", 3})
	a2 := mkAddress("a2", "s1", "", attr{"a1", 3})
	a3 := mkAddress("a1", "s1", "", attr{"a2", 3})
	a4 := mkAddress("a1", "s1", "", attr{"a1", 2})
	a5 := mkAddress("a1", "s1", "", attr{"a1", 4})
	a6 := mkAddress("a1", "s2", "", attr{"a1", 3})
	a7 := mkAddress("a1", "s1", "xx=3", attr{"a1", 3})

	am := newAddressMap()
	for i, a := range []address{a1, a2, a3, a4, a5, a6} {
		am.Set(a, i+1)
	}
	ck(am.Len() == 6, "address map length")
	am.Set(a7, 7)
	ck(am.Len() == 6, "alias address does not grow map")
	if v, ok := am.Get(a1); !ok || v.(int) != 7 {
		ck(false, "alias address returns latest value")
	}
	if _, ok := am.Get(mkAddress("nope", "s1", "", attr{"a1", 3})); ok {
		ck(false, "absent address")
	}
	keys := am.Keys()
	ck(len(keys) == 6, "keys length")
	sortedKeys := make([]string, len(keys))
	for i, k := range keys {
		sortedKeys[i] = k.coreKey()
	}
	ck(sort.StringsAreSorted(sortedKeys), "keys sorted")
	vals := am.Values()
	ints := make([]int, len(vals))
	for i, v := range vals {
		ints[i] = v.(int)
	}
	sort.Ints(ints)
	ck(len(vals) == 6 && ints[0] == 2 && ints[5] == 7, "values match entries")
	am.Delete(a7)
	if _, ok := am.Get(a1); ok {
		ck(false, "delete alias removes core entry")
	}
	am.Delete(mkAddress("missing", "s", ""))
	ck(am.Len() == 5, "delete missing is no-op")

	// 5. EndpointMap：地址集合顺序无关、去重。
	e12 := endpoint{addresses: []address{a1, a2}}
	e21 := endpoint{addresses: []address{a2, a1}}
	e11dup := endpoint{addresses: []address{a1, a1, a2}}
	e123 := endpoint{addresses: []address{a1, a2, a3}}
	em := newEndpointMap()
	em.Set(e12, "x")
	if v, ok := em.Get(e21); !ok || v.(string) != "x" {
		ck(false, "endpoint order-insensitive")
	}
	if v, ok := em.Get(e11dup); !ok || v.(string) != "x" {
		ck(false, "endpoint duplicate addresses normalized")
	}
	em.Set(e123, "y")
	ck(em.Len() == 2, "endpoint map length")
	if _, ok := em.Get(endpoint{addresses: []address{a2}}); ok {
		ck(false, "different endpoint should be absent")
	}
	em.Delete(e21)
	ck(em.Len() == 1, "endpoint delete alias")
	ek := em.Keys()
	ck(len(ek) == 1 && endpointKey(ek[0]) == endpointKey(e123), "endpoint keys")

	// 6. GC：保留解析结果与 map 视图，丢弃临时构建器。
	kept := st.endpoints
	keptKeys := am.Keys()
	keptVals := am.Values()
	st = nil
	gcStress()
	ck(len(kept) == 1 && kept[0].addresses[0].addr == "b:2", "endpoints survive GC")
	ck(len(keptKeys) == 5 && keptKeys[0].coreKey() <= keptKeys[4].coreKey(), "address keys survive GC")
	ck(len(keptVals) == 5, "address values survive GC")
	if reg.Names()[1] != "split" {
		ck(false, "registry names survive GC")
	}

	// 7. 随机 Set/Delete 与模型对账。
	r := rand.New(rand.NewSource(seed))
	model := make(map[string]int)
	ram := newAddressMap()
	pool := make([]address, 24)
	for i := range pool {
		pool[i] = mkAddress("host"+strconv.Itoa(i%8), "s"+strconv.Itoa(i%3),
			"bal"+strconv.Itoa(i%2), attr{"z", int64(i % 5)})
	}
	for i := 0; i < 400; i++ {
		a := pool[r.Intn(len(pool))]
		if r.Intn(10) < 7 {
			v := r.Intn(1000)
			ram.Set(a, v)
			model[a.coreKey()] = v
		} else {
			ram.Delete(a)
			delete(model, a.coreKey())
		}
		if i%50 == 49 {
			gcStress()
			ckSeed(ram.Len() == len(model), "random map length")
			for _, a := range pool {
				want, ok := model[a.coreKey()]
				got, gotOK := ram.Get(a)
				ckSeed(ok == gotOK, "random map presence")
				if ok && gotOK {
					ckSeed(got.(int) == want, "random map value")
				}
			}
			rkeys := ram.Keys()
			rk := make([]string, len(rkeys))
			for j, a := range rkeys {
				rk[j] = a.coreKey()
			}
			ckSeed(sort.StringsAreSorted(rk), "random keys sorted")
			ckSeed(len(rkeys) == len(model), "random keys count")
		}
	}

	finish("l10_resolver_table")
}
