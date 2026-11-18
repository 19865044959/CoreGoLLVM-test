// L10: 生成式 service descriptor 表：服务/方法元数据、path 索引、重复检测、
// 原子注册、handler 闭包跨 GC 调用、流式标记与随机请求分发。
package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
)

var (
	errDupService = errors.New("svc: duplicate service")
	errDupMethod  = errors.New("svc: duplicate method")
	errInvalid    = errors.New("svc: invalid descriptor")
	errNotFound   = errors.New("svc: method not found")
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

type methodDesc struct {
	Name            string
	Handler         func(req []byte) ([]byte, error)
	ClientStreaming bool
	ServerStreaming bool
}

type serviceDesc struct {
	ServiceName string
	HandlerType string
	Methods     []methodDesc
	Metadata    string
}

type registry struct {
	services map[string]*serviceDesc
	byPath   map[string]*methodDesc
}

func newRegistry() *registry {
	return &registry{services: map[string]*serviceDesc{}, byPath: map[string]*methodDesc{}}
}

// register 先全量校验后提交：任何冲突都不留下半注册状态。
func (r *registry) register(sd *serviceDesc) error {
	if sd == nil || sd.ServiceName == "" || sd.HandlerType == "" || len(sd.Methods) == 0 {
		return errInvalid
	}
	if _, ok := r.services[sd.ServiceName]; ok {
		return errDupService
	}
	seen := make(map[string]struct{}, len(sd.Methods))
	for i := range sd.Methods {
		m := &sd.Methods[i]
		if m.Name == "" || m.Handler == nil {
			return errInvalid
		}
		if _, ok := seen[m.Name]; ok {
			return errDupMethod
		}
		seen[m.Name] = struct{}{}
		if _, ok := r.byPath["/"+sd.ServiceName+"/"+m.Name]; ok {
			return errDupMethod
		}
	}
	cp := &serviceDesc{
		ServiceName: sd.ServiceName,
		HandlerType: sd.HandlerType,
		Metadata:    sd.Metadata,
		Methods:     make([]methodDesc, len(sd.Methods)),
	}
	copy(cp.Methods, sd.Methods)
	r.services[cp.ServiceName] = cp
	for i := range cp.Methods {
		r.byPath["/"+cp.ServiceName+"/"+cp.Methods[i].Name] = &cp.Methods[i]
	}
	return nil
}

func (r *registry) serviceNames() []string {
	names := make([]string, 0, len(r.services))
	for k := range r.services {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (r *registry) invoke(path string, req []byte) ([]byte, error) {
	m, ok := r.byPath[path]
	if !ok {
		return nil, errNotFound
	}
	return m.Handler(req)
}

func generatedServices() []*serviceDesc {
	out := make([]*serviceDesc, 0, 8)
	for s := 0; s < 8; s++ {
		svcName := fmt.Sprintf("pkg.Svc%02d", s)
		methods := make([]methodDesc, 16)
		for m := 0; m < 16; m++ {
			si, mi := s, m
			methods[m] = methodDesc{
				Name: fmt.Sprintf("Method%02d", m),
				Handler: func(req []byte) ([]byte, error) {
					return []byte(fmt.Sprintf("s%02d/m%02d/%s", si, mi, string(req))), nil
				},
				ClientStreaming: mi%8 == 0,
				ServerStreaming: mi%4 == 0,
			}
		}
		out = append(out, &serviceDesc{
			ServiceName: svcName,
			HandlerType: svcName + "Handler",
			Methods:     methods,
			Metadata:    "gen.proto",
		})
	}
	return out
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 注册生成的服务表，校验索引规模与排序。
	reg := newRegistry()
	gens := generatedServices()
	for _, sd := range gens {
		if err := reg.register(sd); err != nil {
			ck(false, "register generated service")
		}
	}
	names := reg.serviceNames()
	ck(len(names) == 8, "service count")
	for i, n := range names {
		ck(n == fmt.Sprintf("pkg.Svc%02d", i), "service names sorted")
	}
	ck(len(reg.byPath) == 128, "method path count")
	streams := 0
	clientStreams := 0
	for _, sd := range reg.services {
		ck(len(sd.Methods) == 16, "methods per service")
		ck(sd.Metadata == "gen.proto", "metadata preserved")
		for _, m := range sd.Methods {
			if m.ServerStreaming {
				streams++
			}
			if m.ClientStreaming {
				clientStreams++
			}
		}
	}
	ck(streams == 32, "server streaming method count")
	ck(clientStreams == 16, "client streaming method count")

	// 2. 每个 path 的 handler 分发；GC 后闭包与响应仍正确。
	expected := make(map[string]string, 128)
	for s := 0; s < 8; s++ {
		for m := 0; m < 16; m++ {
			path := fmt.Sprintf("/pkg.Svc%02d/Method%02d", s, m)
			if _, ok := reg.byPath[path]; !ok {
				ck(false, "path lookup")
			}
			expected[path] = fmt.Sprintf("s%02d/m%02d/r%d", s, m, s*16+m)
		}
	}
	var responses [][]byte
	for path, want := range expected {
		got, err := reg.invoke(path, []byte(fmt.Sprintf("r%d", pathIndex(path))))
		if err != nil || string(got) != want {
			ck(false, "handler dispatch")
			break
		}
		responses = append(responses, got)
	}
	gcSink = append(gcSink, responses...)
	gcStress()
	for path, want := range expected {
		got, err := reg.invoke(path, []byte(fmt.Sprintf("r%d", pathIndex(path))))
		if err != nil || string(got) != want {
			ck(false, "handler dispatch after GC")
			break
		}
	}
	if _, err := reg.invoke("/pkg.Nope/Nope", []byte("x")); !errors.Is(err, errNotFound) {
		ck(false, "unknown method error")
	}

	// 3. 重复服务/方法检测与原子性。
	dupSvc := *gens[3]
	dupSvc.Methods = []methodDesc{{Name: "Other", Handler: func([]byte) ([]byte, error) { return nil, nil }}}
	if err := reg.register(&dupSvc); !errors.Is(err, errDupService) {
		ck(false, "duplicate service detected")
	}
	ck(len(reg.services) == 8 && len(reg.byPath) == 128, "duplicate service leaves registry unchanged")

	atomicSvc := &serviceDesc{
		ServiceName: "pkg.Atomic",
		HandlerType: "AtomicHandler",
		Methods: []methodDesc{
			{Name: "A", Handler: func([]byte) ([]byte, error) { return []byte("a"), nil }},
			{Name: "B", Handler: func([]byte) ([]byte, error) { return []byte("b"), nil }},
			{Name: "A", Handler: func([]byte) ([]byte, error) { return []byte("a2"), nil }},
		},
	}
	if err := reg.register(atomicSvc); !errors.Is(err, errDupMethod) {
		ck(false, "intra-service duplicate method detected")
	}
	if _, ok := reg.services["pkg.Atomic"]; ok {
		ck(false, "failed registration leaves no service")
	}
	if _, ok := reg.byPath["/pkg.Atomic/A"]; ok {
		ck(false, "failed registration leaves no path")
	}
	ck(len(reg.services) == 8 && len(reg.byPath) == 128, "failed registration atomic")

	// 4. 非法描述符全部拒绝且不改变注册表。
	invalid := []*serviceDesc{
		nil,
		{ServiceName: "", HandlerType: "H", Methods: []methodDesc{{Name: "A", Handler: func([]byte) ([]byte, error) { return nil, nil }}}},
		{ServiceName: "pkg.Bad", HandlerType: "", Methods: []methodDesc{{Name: "A", Handler: func([]byte) ([]byte, error) { return nil, nil }}}},
		{ServiceName: "pkg.Bad", HandlerType: "H", Methods: nil},
		{ServiceName: "pkg.Bad", HandlerType: "H", Methods: []methodDesc{{Name: "", Handler: func([]byte) ([]byte, error) { return nil, nil }}}},
		{ServiceName: "pkg.Bad", HandlerType: "H", Methods: []methodDesc{{Name: "A"}}},
	}
	for _, sd := range invalid {
		if err := reg.register(sd); !errors.Is(err, errInvalid) {
			ck(false, "invalid descriptor rejected")
		}
	}
	ck(len(reg.services) == 8 && len(reg.byPath) == 128, "invalid descriptors leave registry unchanged")

	// 5. 不同服务可用同名方法；注册后调用方修改原描述符不影响注册表。
	newSvc := &serviceDesc{
		ServiceName: "pkg.Extra",
		HandlerType: "ExtraHandler",
		Metadata:    "extra.proto",
		Methods: []methodDesc{
			{Name: "Method00", Handler: func(req []byte) ([]byte, error) { return []byte("extra:" + string(req)), nil }},
			{Name: "Method01", Handler: func(req []byte) ([]byte, error) { return []byte("x"), nil }},
		},
	}
	ck(reg.register(newSvc) == nil, "register extra service")
	newSvc.Methods[0].Name = "Hacked"
	newSvc.Methods[0].Handler = func([]byte) ([]byte, error) { return []byte("hacked"), nil }
	got, err := reg.invoke("/pkg.Extra/Method00", []byte("q"))
	ck(err == nil && string(got) == "extra:q", "registry decoupled from caller mutation")
	if _, err := reg.invoke("/pkg.Extra/Hacked", nil); !errors.Is(err, errNotFound) {
		ck(false, "mutated method name not registered")
	}
	ck(len(reg.services) == 9 && len(reg.byPath) == 130, "extra service index")

	// 6. UAF_SEED 随机请求分发；周期性 GC。
	rng := rand.New(rand.NewSource(curSeed))
	for i := 0; i < 600; i++ {
		s := rng.Intn(8)
		m := rng.Intn(16)
		req := fmt.Sprintf("q%d", rng.Intn(100000))
		path := fmt.Sprintf("/pkg.Svc%02d/Method%02d", s, m)
		want := fmt.Sprintf("s%02d/m%02d/%s", s, m, req)
		out, err := reg.invoke(path, []byte(req))
		ckSeed(err == nil && string(out) == want, "random service dispatch")
		if i%97 == 0 {
			gcStress()
			out2, err2 := reg.invoke(path, []byte(req))
			ckSeed(err2 == nil && string(out2) == want, "random dispatch after GC")
		}
	}
	ck(len(reg.byPath) == 130, "path count stable after random dispatch")

	finish("l10_service_desc_table")
}

// pathIndex 从 "/pkg.SvcNN/MethodMM" 提取 NN*16+MM，用于构造期望响应。
func pathIndex(path string) int {
	var svc, method int
	fmt.Sscanf(path, "/pkg.Svc%d/Method%d", &svc, &method)
	return svc*16 + method
}
