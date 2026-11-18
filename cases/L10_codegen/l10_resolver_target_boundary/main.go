// l10_resolver_target_boundary: resolver target grammar (scheme/authority/
// endpoint, IPv6, percent escapes, passthrough fallback), scheme builder
// registry, endpoint address de-duplication with weighted schedules, and a
// generation-guarded resolver state that ignores stale updates.
package main

import (
	"fmt"
	"math/rand"
	"net"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

var (
	failed int
	seed   = int64(1)
	gcSink [][]byte
)

func ck(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Println("FAIL:", msg)
	}
}

func ckSeed(ok bool, msg string) {
	if !ok {
		failed++
		fmt.Printf("FAIL: %s (UAF_SEED=%d)\n", msg, seed)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 4096)
		b[0] = byte(i)
		b[len(b)-1] = byte(i * 29)
		gcSink = append(gcSink, b)
	}
	for i := range gcSink {
		gcSink[i] = nil
	}
	gcSink = gcSink[:0]
	runtime.GC()
	runtime.GC()
}

func finish(name string) {
	if failed != 0 {
		fmt.Printf("FAILED %s (%d failed checks)\n", name, failed)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func readSeed() int64 {
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
	if err != nil || v < 0 {
		return def
	}
	return v
}

// ---------- target grammar ----------

type target struct {
	scheme    string
	authority string
	endpoint  string
	canonical string
	fallback  bool
}

func validScheme(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if i == 0 {
			if c < 'a' || c > 'z' {
				return false
			}
			continue
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '-' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func invalidRaw(raw string) bool {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c <= ' ' || c == 0x7f || c == '?' || c == '#' {
			return true
		}
	}
	return false
}

func validAuthority(a string) bool {
	if strings.Contains(a, "@") {
		return false
	}
	if strings.ContainsAny(a, "[]") {
		if len(a) < 3 || a[0] != '[' || a[len(a)-1] != ']' {
			return false
		}
		inner := a[1 : len(a)-1]
		if inner == "" || strings.ContainsAny(inner, "[]") {
			return false
		}
	}
	return true
}

func parseTarget(raw string, known func(string) bool) (target, error) {
	if raw == "" {
		return target{}, fmt.Errorf("empty target")
	}
	if invalidRaw(raw) {
		return target{}, fmt.Errorf("invalid character in target")
	}
	fallback := func() target {
		return target{
			scheme:    "passthrough",
			endpoint:  raw,
			canonical: "passthrough:///" + url.PathEscape(raw),
			fallback:  true,
		}
	}
	if i := strings.Index(raw, "://"); i >= 0 {
		schemeRaw := raw[:i]
		if !validScheme(schemeRaw) {
			return target{}, fmt.Errorf("invalid scheme %q", schemeRaw)
		}
		scheme := strings.ToLower(schemeRaw)
		if !known(scheme) {
			return fallback(), nil
		}
		rest := raw[i+3:]
		auth := ""
		epRaw := rest
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			auth = rest[:j]
			epRaw = rest[j+1:]
		} else {
			auth = rest
			epRaw = ""
		}
		if !validAuthority(auth) {
			return target{}, fmt.Errorf("invalid authority %q", auth)
		}
		ep, err := url.PathUnescape(epRaw)
		if err != nil {
			return target{}, fmt.Errorf("bad endpoint escape: %w", err)
		}
		if invalidRaw(ep) {
			return target{}, fmt.Errorf("invalid decoded endpoint")
		}
		return target{
			scheme:    scheme,
			authority: auth,
			endpoint:  ep,
			canonical: scheme + "://" + auth + "/" + url.PathEscape(ep),
		}, nil
	}
	// No "://": a registered leading scheme followed by ':' is accepted;
	// anything else falls back to passthrough (matching gRPC behavior).
	if i := strings.IndexByte(raw, ':'); i >= 0 {
		schemeRaw := raw[:i]
		if validScheme(schemeRaw) && known(strings.ToLower(schemeRaw)) {
			scheme := strings.ToLower(schemeRaw)
			ep, err := url.PathUnescape(raw[i+1:])
			if err != nil {
				return target{}, fmt.Errorf("bad endpoint escape: %w", err)
			}
			return target{
				scheme:    scheme,
				endpoint:  ep,
				canonical: scheme + ":///" + url.PathEscape(ep),
			}, nil
		}
	}
	return fallback(), nil
}

// ---------- scheme registry / builders ----------

var (
	errDuplicateScheme = fmt.Errorf("duplicate scheme")
	errFrozenRegistry  = fmt.Errorf("resolver registry frozen")
)

type builderFunc func(t target) ([]string, error)

type schemeRegistry struct {
	m      map[string]builderFunc
	frozen bool
}

func newSchemeRegistry() *schemeRegistry {
	return &schemeRegistry{m: map[string]builderFunc{}}
}

func (r *schemeRegistry) Register(scheme string, b builderFunc) error {
	if !validScheme(scheme) || b == nil {
		return fmt.Errorf("invalid scheme registration")
	}
	s := strings.ToLower(scheme)
	if r.frozen {
		return errFrozenRegistry
	}
	if _, ok := r.m[s]; ok {
		return errDuplicateScheme
	}
	r.m[s] = b
	return nil
}

func (r *schemeRegistry) known(scheme string) bool {
	_, ok := r.m[strings.ToLower(scheme)]
	return ok
}

func (r *schemeRegistry) Freeze() { r.frozen = true }

func builderPassthrough(t target) ([]string, error) {
	if t.endpoint == "" {
		return nil, fmt.Errorf("passthrough: empty endpoint")
	}
	return []string{t.endpoint}, nil
}

func builderDNS(t target) ([]string, error) {
	src := t.endpoint
	if src == "" {
		src = t.authority
	}
	if src == "" {
		return nil, fmt.Errorf("dns: empty endpoint and authority")
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(src, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("dns: no addresses")
	}
	return out, nil
}

func builderStatic(t target) ([]string, error) {
	if t.endpoint == "" {
		return nil, fmt.Errorf("static: empty endpoint")
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(t.endpoint, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("static: empty address")
		}
		if _, _, err := net.SplitHostPort(part); err != nil {
			return nil, fmt.Errorf("static: bad address %q", part)
		}
		if seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("static: no addresses")
	}
	return out, nil
}

func builderUnix(t target) ([]string, error) {
	if t.endpoint == "" {
		return nil, fmt.Errorf("unix: empty socket path")
	}
	// The URI authority/path split drops the separator slash; unix socket
	// paths are absolute, so restore it.
	p := t.endpoint
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return []string{p}, nil
}

func defaultRegistry() *schemeRegistry {
	r := newSchemeRegistry()
	_ = r.Register("passthrough", builderPassthrough)
	_ = r.Register("dns", builderDNS)
	_ = r.Register("static", builderStatic)
	_ = r.Register("unix", builderUnix)
	return r
}

type resolution struct {
	tgt      target
	addrs    []string
	fallback bool
}

func (r *schemeRegistry) Resolve(raw string) (resolution, error) {
	t, err := parseTarget(raw, r.known)
	if err != nil {
		return resolution{}, err
	}
	b, ok := r.m[t.scheme]
	if !ok {
		return resolution{}, fmt.Errorf("no builder for scheme %q", t.scheme)
	}
	addrs, err := b(t)
	if err != nil {
		return resolution{}, err
	}
	return resolution{tgt: t, addrs: addrs, fallback: t.fallback}, nil
}

// ---------- endpoint expansion ----------

type endpointSpec struct {
	addrs    []string
	weight   int
	priority int
}

type listedAddr struct {
	addr     string
	priority int
	endpoint int
}

func expandEndpoints(specs []endpointSpec) ([]listedAddr, error) {
	for i, sp := range specs {
		if sp.weight <= 0 {
			return nil, fmt.Errorf("endpoint %d: non-positive weight", i)
		}
		if sp.priority < 0 {
			return nil, fmt.Errorf("endpoint %d: negative priority", i)
		}
		if len(sp.addrs) == 0 {
			return nil, fmt.Errorf("endpoint %d: no addresses", i)
		}
		seen := map[string]bool{}
		for _, a := range sp.addrs {
			if a == "" {
				return nil, fmt.Errorf("endpoint %d: empty address", i)
			}
			if seen[a] {
				return nil, fmt.Errorf("endpoint %d: duplicate address %q", i, a)
			}
			seen[a] = true
		}
	}
	order := make([]int, len(specs))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return specs[order[i]].priority < specs[order[j]].priority
	})
	seenGlobal := map[string]bool{}
	var out []listedAddr
	for _, idx := range order {
		sp := specs[idx]
		for rep := 0; rep < sp.weight; rep++ {
			for _, a := range sp.addrs {
				if seenGlobal[a] {
					continue
				}
				out = append(out, listedAddr{addr: a, priority: sp.priority, endpoint: idx})
			}
		}
		for _, a := range sp.addrs {
			seenGlobal[a] = true
		}
	}
	return out, nil
}

var errStaleUpdate = fmt.Errorf("stale resolver update")

type resolverState struct {
	gen     uint64
	list    []listedAddr
	applied int
}

func (s *resolverState) Apply(gen uint64, specs []endpointSpec) error {
	if gen <= s.gen {
		return errStaleUpdate
	}
	list, err := expandEndpoints(specs)
	if err != nil {
		return err
	}
	s.gen = gen
	s.list = list
	s.applied++
	return nil
}

func (s *resolverState) Snapshot() []listedAddr {
	return append([]listedAddr(nil), s.list...)
}

func addrsOf(list []listedAddr) []string {
	out := make([]string, len(list))
	for i := range list {
		out[i] = list[i].addr
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------- main ----------

func main() {
	seed = readSeed()
	reg := defaultRegistry()

	// 1. Target grammar golden cases.
	type targetCase struct {
		raw       string
		scheme    string
		authority string
		endpoint  string
		canonical string
		fallback  bool
	}
	cases := []targetCase{
		{"dns:///host:53", "dns", "", "host:53", "dns:///host:53", false},
		{"dns://auth/host", "dns", "auth", "host", "dns://auth/host", false},
		{"dns:host:53", "dns", "", "host:53", "dns:///host:53", false},
		{"passthrough:///x", "passthrough", "", "x", "passthrough:///x", false},
		{"no.scheme", "passthrough", "", "no.scheme", "passthrough:///no.scheme", true},
		{"nonexist:///non.existent", "passthrough", "", "nonexist:///non.existent", "passthrough:///nonexist:%2F%2F%2Fnon.existent", true},
		{"unknown:opaque", "passthrough", "", "unknown:opaque", "passthrough:///unknown:opaque", true},
		{"dns:///na%2Fme", "dns", "", "na/me", "dns:///na%2Fme", false},
		{"dns://[::1]/svc", "dns", "[::1]", "svc", "dns://[::1]/svc", false},
		{"dns://auth/", "dns", "auth", "", "dns://auth/", false},
		{"static:///[::1]:1,[::2]:2", "static", "", "[::1]:1,[::2]:2", "static:///%5B::1%5D:1%2C%5B::2%5D:2", false},
		{"unix:///tmp/x.sock", "unix", "", "tmp/x.sock", "unix:///tmp%2Fx.sock", false},
	}
	for i, tc := range cases {
		t, err := parseTarget(tc.raw, reg.known)
		ck(err == nil, fmt.Sprintf("parse case %d", i))
		if err != nil {
			continue
		}
		ck(t.scheme == tc.scheme && t.authority == tc.authority && t.endpoint == tc.endpoint && t.canonical == tc.canonical && t.fallback == tc.fallback,
			fmt.Sprintf("parse case %d: %+v", i, t))
		// Canonical form reparses to the same scheme/authority/endpoint.
		t2, err := parseTarget(t.canonical, reg.known)
		ck(err == nil && t2.scheme == t.scheme && t2.authority == t.authority && t2.endpoint == t.endpoint && t2.canonical == t.canonical,
			fmt.Sprintf("canonical reparse %d: %+v", i, t2))
	}

	// 2. Invalid targets.
	badTargets := []string{
		"",
		" ",
		"dns:///a b",
		"://x",
		"1abc:///x",
		"+abc:///x",
		"dns:///x?y",
		"dns:///x#y",
		"dns://[::1/svc",
		"dns://::1]/svc",
		"dns://user@auth/x",
		"dns:///bad%E0%A4%A",
		"dns:///x\x00y",
	}
	for i, raw := range badTargets {
		_, err := parseTarget(raw, reg.known)
		ck(err != nil, fmt.Sprintf("bad target %d rejected", i))
	}
	// Unknown-scheme fallbacks are opaque, including percent sequences.
	fb, err := parseTarget("unknown:///x%zz", reg.known)
	ck(err == nil && fb.fallback && fb.endpoint == "unknown:///x%zz", "opaque fallback preserved")

	// 3. Scheme registry behavior.
	ck(reg.known("dns") && reg.known("static") && reg.known("unix") && reg.known("passthrough"), "default schemes registered")
	ck(reg.Register("dns", builderDNS) == errDuplicateScheme, "duplicate scheme rejected")
	ck(reg.Register("", builderDNS) != nil, "empty scheme rejected")
	ck(reg.Register("x", nil) != nil, "nil builder rejected")
	reg2 := defaultRegistry()
	reg2.Freeze()
	ck(reg2.Register("new", builderDNS) == errFrozenRegistry, "frozen registry rejects registration")
	if _, err := reg2.Resolve("dns:///a"); err != nil {
		ck(false, "frozen registry still resolves")
	}

	// 4. Builder behavior.
	resolveCases := []struct {
		raw       string
		want      []string
		fallback  bool
		wantError bool
	}{
		{"dns:///A,a,b,A", []string{"A", "b"}, false, false},
		{"dns://auth/", []string{"auth"}, false, false},
		{"dns:///", nil, false, true},
		{"static:///127.0.0.1:1,[::1]:2,127.0.0.1:1", []string{"127.0.0.1:1", "[::1]:2"}, false, false},
		{"static:///127.0.0.1", nil, false, true},
		{"static:///", nil, false, true},
		{"unix:///tmp/x.sock", []string{"/tmp/x.sock"}, false, false},
		{"unix://host", nil, false, true},
		{"passthrough:///name", []string{"name"}, false, false},
		{"passthrough:///", nil, false, true},
		{"unknown:///x", []string{"unknown:///x"}, true, false},
		{"no.scheme", []string{"no.scheme"}, true, false},
	}
	for i, rc := range resolveCases {
		res, err := reg.Resolve(rc.raw)
		if rc.wantError {
			ck(err != nil, fmt.Sprintf("resolve error case %d", i))
			continue
		}
		ck(err == nil && equalStrings(res.addrs, rc.want) && res.fallback == rc.fallback, fmt.Sprintf("resolve case %d: %v err=%v", i, res.addrs, err))
	}

	// 5. Endpoint expansion: weights, priorities, de-duplication.
	expandCases := []struct {
		name string
		in   []endpointSpec
		want []string
		bad  bool
	}{
		{"weights", []endpointSpec{{[]string{"A"}, 3, 0}, {[]string{"B"}, 1, 0}}, []string{"A", "A", "A", "B"}, false},
		{"multi-address weight", []endpointSpec{{[]string{"A", "B"}, 2, 0}}, []string{"A", "B", "A", "B"}, false},
		{"cross-endpoint dedup", []endpointSpec{{[]string{"A"}, 2, 0}, {[]string{"A", "B"}, 1, 0}}, []string{"A", "A", "B"}, false},
		{"priority order", []endpointSpec{{[]string{"B"}, 1, 2}, {[]string{"A"}, 1, 0}, {[]string{"C"}, 1, 1}}, []string{"A", "C", "B"}, false},
		{"empty", nil, nil, false},
		{"zero weight", []endpointSpec{{[]string{"A"}, 0, 0}}, nil, true},
		{"negative weight", []endpointSpec{{[]string{"A"}, -1, 0}}, nil, true},
		{"no addrs", []endpointSpec{{nil, 1, 0}}, nil, true},
		{"empty addr", []endpointSpec{{[]string{""}, 1, 0}}, nil, true},
		{"duplicate addr", []endpointSpec{{[]string{"A", "A"}, 1, 0}}, nil, true},
		{"negative priority", []endpointSpec{{[]string{"A"}, 1, -1}}, nil, true},
	}
	for i, ec := range expandCases {
		got, err := expandEndpoints(ec.in)
		if ec.bad {
			ck(err != nil, "expand reject: "+ec.name)
			continue
		}
		ck(err == nil && equalStrings(addrsOf(got), ec.want), fmt.Sprintf("expand %d %s: %v", i, ec.name, addrsOf(got)))
	}

	// 6. Generation guard: stale updates are ignored, failures are atomic.
	var st resolverState
	err = st.Apply(1, []endpointSpec{{[]string{"A"}, 2, 1}, {[]string{"B"}, 1, 0}})
	ck(err == nil && equalStrings(addrsOf(st.Snapshot()), []string{"B", "A", "A"}), "initial state apply")
	before := addrsOf(st.Snapshot())
	ck(st.Apply(1, []endpointSpec{{[]string{"Z"}, 1, 0}}) == errStaleUpdate, "same generation rejected")
	ck(st.Apply(0, []endpointSpec{{[]string{"Z"}, 1, 0}}) == errStaleUpdate, "older generation rejected")
	c1 := addrsOf(st.Snapshot())
	ck(equalStrings(c1, before), "stale update leaves state unchanged")
	err = st.Apply(2, []endpointSpec{{[]string{"C"}, 1, 0}})
	ck(err == nil && equalStrings(addrsOf(st.Snapshot()), []string{"C"}), "new generation replaces addresses")
	err = st.Apply(3, []endpointSpec{{[]string{"D"}, 0, 0}})
	ck(err != nil, "invalid update rejected")
	ck(st.gen == 2 && equalStrings(addrsOf(st.Snapshot()), []string{"C"}), "failed update leaves generation and addresses")
	gcStress()
	ck(equalStrings(addrsOf(st.Snapshot()), []string{"C"}), "state survives GC")
	ck(st.Apply(4, nil) == nil && len(st.Snapshot()) == 0, "empty update clears addresses")

	// 7. Seeded torture: parse canonical roundtrips and expansion models.
	rounds := envInt("TORTURE_ROUNDS", 300)
	r := rand.New(rand.NewSource(seed))
	okRounds := 0
	for i := 0; i < rounds; i++ {
		var raw string
		switch r.Intn(7) {
		case 0:
			raw = fmt.Sprintf("dns:///host%d:%d", r.Intn(1000), 1000+r.Intn(5000))
		case 1:
			raw = fmt.Sprintf("dns://auth%d/host%d", r.Intn(100), r.Intn(1000))
		case 2:
			raw = fmt.Sprintf("dns:host%d:%d", r.Intn(1000), 1000+r.Intn(5000))
		case 3:
			raw = fmt.Sprintf("static:///127.0.0.%d:%d", 1+r.Intn(250), 1000+r.Intn(5000))
		case 4:
			raw = fmt.Sprintf("unix:///tmp/sock%d.sock", r.Intn(1000))
		case 5:
			raw = fmt.Sprintf("passthrough:///name%d", r.Intn(1000))
		default:
			raw = fmt.Sprintf("unknown%d:///endpoint%d", r.Intn(100), r.Intn(1000))
		}
		t, err := parseTarget(raw, reg.known)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture parse %d: %v", i, err))
			continue
		}
		t2, err := parseTarget(t.canonical, reg.known)
		if err != nil || t2.scheme != t.scheme || t2.authority != t.authority || t2.endpoint != t.endpoint || t2.canonical != t.canonical {
			ckSeed(false, fmt.Sprintf("torture canonical %d", i))
			continue
		}
		// Expansion model: unique addresses, so the schedule is a pure
		// stable-sort plus weight repetition.
		n := 1 + r.Intn(4)
		specs := make([]endpointSpec, n)
		for j := 0; j < n; j++ {
			addrCount := 1 + r.Intn(3)
			addrs := make([]string, addrCount)
			for k := range addrs {
				addrs[k] = fmt.Sprintf("e%d_%d_%d", i, j, k)
			}
			specs[j] = endpointSpec{addrs: addrs, weight: 1 + r.Intn(3), priority: r.Intn(3)}
		}
		got, err := expandEndpoints(specs)
		if err != nil {
			ckSeed(false, fmt.Sprintf("torture expand %d: %v", i, err))
			continue
		}
		var model []string
		order := make([]int, n)
		for j := range order {
			order[j] = j
		}
		sort.SliceStable(order, func(a, b int) bool { return specs[order[a]].priority < specs[order[b]].priority })
		for _, idx := range order {
			for rep := 0; rep < specs[idx].weight; rep++ {
				model = append(model, specs[idx].addrs...)
			}
		}
		if !equalStrings(addrsOf(got), model) {
			ckSeed(false, fmt.Sprintf("torture schedule %d: %v vs %v", i, addrsOf(got), model))
			continue
		}
		if i%29 == 0 {
			gcStress()
		}
		okRounds++
	}
	ck(okRounds == rounds, "torture: all parse/expand rounds matched")

	// 8. Resolver state and targets across GC.
	gcStress()
	res, err := reg.Resolve("dns:///a,b,a")
	ck(err == nil && equalStrings(res.addrs, []string{"a", "b"}), "resolve after GC")
	ck(res.tgt.canonical == "dns:///a,b,a" || res.tgt.endpoint == "a,b,a", "target payload survives GC")

	finish("l10_resolver_target_boundary")
}
