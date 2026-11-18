// L10: 生成式描述符注册表重复检测：文件级原子注册、跨文件/跨 symbol 种类冲突、
// 冲突名排序、重复文件拒绝、GC 后注册表稳定，TORTURE_ROUNDS 放大随机注册。
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
	errDupFile  = errors.New("registry: duplicate file")
	errConflict = errors.New("registry: symbol conflict")
	errInvalid  = errors.New("registry: invalid descriptor")
)

type conflictError struct {
	Names []string
}

func (e *conflictError) Error() string {
	return "registry: symbol conflict: " + strings.Join(e.Names, ",")
}

func (e *conflictError) Is(target error) bool { return target == errConflict }

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

func envRounds() int {
	n := 1
	if s := os.Getenv("TORTURE_ROUNDS"); s != "" {
		if v, err := strconv.Atoi(s); err == nil && v > 0 {
			n = v
		}
	}
	if n > 50 {
		n = 50
	}
	return n
}

type enumDesc struct {
	Name string
}

type msgDesc struct {
	Name   string
	Fields []string
}

type fileDesc struct {
	Path     string
	Package  string
	Messages []*msgDesc
	Enums    []*enumDesc
}

type registry struct {
	files    map[string]*fileDesc
	msgs     map[string]*msgDesc
	enums    map[string]*enumDesc
	bySymbol map[string]string
}

func newRegistry() *registry {
	return &registry{
		files:    map[string]*fileDesc{},
		msgs:     map[string]*msgDesc{},
		enums:    map[string]*enumDesc{},
		bySymbol: map[string]string{},
	}
}

func fullName(pkg, name string) string { return pkg + "." + name }

// Register 先收集全部冲突，再一次性提交；任何错误都不改变注册表。
func (r *registry) Register(fd *fileDesc) error {
	if fd == nil || fd.Path == "" || fd.Package == "" {
		return errInvalid
	}
	for _, m := range fd.Messages {
		if m == nil || m.Name == "" {
			return errInvalid
		}
	}
	for _, e := range fd.Enums {
		if e == nil || e.Name == "" {
			return errInvalid
		}
	}
	if _, ok := r.files[fd.Path]; ok {
		return errDupFile
	}

	conflicts := map[string]struct{}{}
	seen := map[string]struct{}{}
	for _, m := range fd.Messages {
		full := fullName(fd.Package, m.Name)
		if _, dup := seen[full]; dup {
			conflicts[full] = struct{}{}
		}
		seen[full] = struct{}{}
		if _, exists := r.bySymbol[full]; exists {
			conflicts[full] = struct{}{}
		}
	}
	for _, e := range fd.Enums {
		full := fullName(fd.Package, e.Name)
		if _, dup := seen[full]; dup {
			conflicts[full] = struct{}{}
		}
		seen[full] = struct{}{}
		if _, exists := r.bySymbol[full]; exists {
			conflicts[full] = struct{}{}
		}
	}
	if len(conflicts) > 0 {
		names := make([]string, 0, len(conflicts))
		for n := range conflicts {
			names = append(names, n)
		}
		sort.Strings(names)
		return &conflictError{Names: names}
	}

	cp := &fileDesc{Path: fd.Path, Package: fd.Package}
	cp.Messages = make([]*msgDesc, 0, len(fd.Messages))
	cp.Enums = make([]*enumDesc, 0, len(fd.Enums))
	for _, m := range fd.Messages {
		fields := make([]string, len(m.Fields))
		copy(fields, m.Fields)
		cp.Messages = append(cp.Messages, &msgDesc{Name: m.Name, Fields: fields})
	}
	for _, e := range fd.Enums {
		cp.Enums = append(cp.Enums, &enumDesc{Name: e.Name})
	}
	r.files[cp.Path] = cp
	for _, m := range cp.Messages {
		full := fullName(cp.Package, m.Name)
		r.msgs[full] = m
		r.bySymbol[full] = "message"
	}
	for _, e := range cp.Enums {
		full := fullName(cp.Package, e.Name)
		r.enums[full] = e
		r.bySymbol[full] = "enum"
	}
	return nil
}

func (r *registry) Message(full string) (*msgDesc, bool) {
	m, ok := r.msgs[full]
	return m, ok
}

func (r *registry) Enum(full string) (*enumDesc, bool) {
	e, ok := r.enums[full]
	return e, ok
}

func (r *registry) Files() []string {
	out := make([]string, 0, len(r.files))
	for p := range r.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (r *registry) Symbols() []string {
	out := make([]string, 0, len(r.bySymbol))
	for s := range r.bySymbol {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func generatedFile(pkgIdx, msgCount, enumCount int) *fileDesc {
	pkg := fmt.Sprintf("pkg%02d", pkgIdx)
	fd := &fileDesc{Path: fmt.Sprintf("gen/pkg%02d.proto", pkgIdx), Package: pkg}
	for m := 0; m < msgCount; m++ {
		fd.Messages = append(fd.Messages, &msgDesc{
			Name:   fmt.Sprintf("Msg%02d", m),
			Fields: []string{"id", "name", fmt.Sprintf("f%02d", m)},
		})
	}
	for e := 0; e < enumCount; e++ {
		fd.Enums = append(fd.Enums, &enumDesc{Name: fmt.Sprintf("Enum%02d", e)})
	}
	return fd
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	tRounds := envRounds()

	// 1. 批量注册生成文件。
	reg := newRegistry()
	gens := make([]*fileDesc, 0, 40)
	for i := 0; i < 40; i++ {
		fd := generatedFile(i, 6, 2)
		gens = append(gens, fd)
		if err := reg.Register(fd); err != nil {
			ck(false, "register generated file")
		}
	}
	ck(len(reg.files) == 40, "file count")
	ck(len(reg.msgs) == 240, "message count")
	ck(len(reg.enums) == 80, "enum count")
	ck(len(reg.bySymbol) == 320, "symbol count")
	files := reg.Files()
	if len(files) != 40 || files[0] != "gen/pkg00.proto" || files[39] != "gen/pkg39.proto" {
		ck(false, "file list sorted")
	}
	syms := reg.Symbols()
	if len(syms) != 320 {
		ck(false, "symbol list size")
	} else {
		sortedOK := true
		for i := 1; i < len(syms); i++ {
			if syms[i-1] >= syms[i] {
				sortedOK = false
				break
			}
		}
		ck(sortedOK, "symbol list sorted and unique")
	}
	m, ok := reg.Message("pkg07.Msg03")
	ck(ok && m.Name == "Msg03" && len(m.Fields) == 3 && m.Fields[2] == "f03", "message lookup")
	e, ok := reg.Enum("pkg39.Enum01")
	ck(ok && e.Name == "Enum01", "enum lookup")
	if _, ok := reg.Message("pkg39.Enum01"); ok {
		ck(false, "enum not in message namespace")
	}
	if _, ok := reg.Enum("pkg07.Msg03"); ok {
		ck(false, "message not in enum namespace")
	}

	// 2. 重复文件路径：拒绝且不合并任何符号。
	dupPath := &fileDesc{Path: "gen/pkg07.proto", Package: "other",
		Messages: []*msgDesc{{Name: "Unique"}}}
	if err := reg.Register(dupPath); !errors.Is(err, errDupFile) {
		ck(false, "duplicate file detected")
	}
	ck(len(reg.files) == 40 && len(reg.bySymbol) == 320, "duplicate file atomic")
	if _, ok := reg.Message("other.Unique"); ok {
		ck(false, "duplicate file symbol not registered")
	}

	// 3. 跨文件符号冲突：同文件中的唯一符号也不得部分注册。
	conflict := &fileDesc{Path: "gen/conflict.proto", Package: "pkg07",
		Messages: []*msgDesc{{Name: "Msg03"}, {Name: "BrandNew"}},
		Enums:    []*enumDesc{{Name: "FreshEnum"}}}
	err := reg.Register(conflict)
	if !errors.Is(err, errConflict) {
		ck(false, "cross-file conflict detected")
	}
	var ce *conflictError
	if errors.As(err, &ce) {
		ck(len(ce.Names) == 1 && ce.Names[0] == "pkg07.Msg03", "conflict name reported")
	} else {
		ck(false, "conflict error type")
	}
	ck(len(reg.files) == 40 && len(reg.msgs) == 240 && len(reg.enums) == 80, "conflict atomic")
	if _, ok := reg.Message("pkg07.BrandNew"); ok {
		ck(false, "unique message not registered on conflict")
	}
	if _, ok := reg.Enum("pkg07.FreshEnum"); ok {
		ck(false, "unique enum not registered on conflict")
	}

	// 4. 文件内重复 + 跨 symbol 种类冲突。
	intra := &fileDesc{Path: "gen/intra.proto", Package: "pkgX",
		Messages: []*msgDesc{{Name: "Dup"}, {Name: "Dup"}}}
	err = reg.Register(intra)
	ce = nil
	if !errors.Is(err, errConflict) || !errors.As(err, &ce) || len(ce.Names) != 1 || ce.Names[0] != "pkgX.Dup" {
		ck(false, "intra-file duplicate detected")
	}
	crossKind := &fileDesc{Path: "gen/ck.proto", Package: "pkgX",
		Messages: []*msgDesc{{Name: "Shared"}},
		Enums:    []*enumDesc{{Name: "Shared"}}}
	err = reg.Register(crossKind)
	if !errors.Is(err, errConflict) {
		ck(false, "cross-kind duplicate detected")
	}
	ck(len(reg.files) == 40 && len(reg.bySymbol) == 320, "intra conflicts atomic")

	// 5. 与已注册 symbol 跨种类冲突。
	crossKind2 := &fileDesc{Path: "gen/ck2.proto", Package: "pkg07",
		Enums: []*enumDesc{{Name: "Msg00"}}}
	if err := reg.Register(crossKind2); !errors.Is(err, errConflict) {
		ck(false, "enum conflicts with existing message")
	}

	// 6. 多冲突名称必须去重并排序。
	multi := &fileDesc{Path: "gen/multi.proto", Package: "pkg07",
		Messages: []*msgDesc{{Name: "Msg05"}, {Name: "Msg01"}, {Name: "Msg05"}},
		Enums:    []*enumDesc{{Name: "Enum00"}, {Name: "Msg09"}}}
	err = reg.Register(multi)
	ce = nil
	if !errors.Is(err, errConflict) || !errors.As(err, &ce) {
		ck(false, "multi conflict detected")
	} else {
		want := []string{"pkg07.Enum00", "pkg07.Msg01", "pkg07.Msg05"}
		ck(len(ce.Names) == len(want), "multi conflict count")
		for i := range want {
			if i < len(ce.Names) && ce.Names[i] != want[i] {
				ck(false, "multi conflict sorted")
			}
		}
	}

	// 7. 非法描述符全部拒绝。
	invalid := []*fileDesc{
		nil,
		{Path: "", Package: "p"},
		{Path: "x.proto", Package: ""},
		{Path: "x.proto", Package: "p", Messages: []*msgDesc{nil}},
		{Path: "x.proto", Package: "p", Messages: []*msgDesc{{Name: ""}}},
		{Path: "x.proto", Package: "p", Enums: []*enumDesc{nil}},
		{Path: "x.proto", Package: "p", Enums: []*enumDesc{{Name: ""}}},
	}
	for _, fd := range invalid {
		if err := reg.Register(fd); !errors.Is(err, errInvalid) {
			ck(false, "invalid descriptor rejected")
		}
	}
	ck(len(reg.files) == 40 && len(reg.bySymbol) == 320, "invalid descriptors atomic")

	// 8. 注册后修改调用方描述符不影响注册表。
	gens[0].Path = "mutated.proto"
	gens[0].Messages[0].Name = "Mutated"
	gens[0].Messages[0].Fields[0] = "mutated"
	if _, ok := reg.files["gen/pkg00.proto"]; !ok {
		ck(false, "file path decoupled")
	}
	m, ok = reg.Message("pkg00.Msg00")
	ck(ok && m.Name == "Msg00" && m.Fields[0] == "id", "message decoupled from caller")

	// 9. GC 后注册表与描述符内容稳定。
	gcSink = append(gcSink, []byte("registry"))
	gcStress()
	ck(len(reg.files) == 40 && len(reg.msgs) == 240 && len(reg.enums) == 80 && len(reg.bySymbol) == 320, "registry stable after GC")
	m, ok = reg.Message("pkg23.Msg04")
	ck(ok && m.Fields[2] == "f04", "message content stable after GC")
	ck(len(reg.Symbols()) == 320, "symbol listing stable after GC")

	// 10. UAF_SEED 随机注册：独立模型校验成功/失败与冲突集合。
	r := rand.New(rand.NewSource(curSeed))
	type held struct {
		reg  *registry
		msgs int
		syms int
	}
	var keep []held
	rounds := 70 * tRounds
	for round := 0; round < rounds; round++ {
		rr := newRegistry()
		model := map[string]string{}
		paths := map[string]bool{}
		nf := 3 + r.Intn(6)
		for f := 0; f < nf; f++ {
			pkg := fmt.Sprintf("p%d", r.Intn(4))
			path := fmt.Sprintf("f%d.proto", r.Intn(6))
			fd := &fileDesc{Path: path, Package: pkg}
			nm := 1 + r.Intn(4)
			forceLocalDup := r.Intn(6) == 0
			for i := 0; i < nm; i++ {
				name := fmt.Sprintf("M%d", r.Intn(6))
				if forceLocalDup && i > 0 && r.Intn(2) == 0 {
					name = fd.Messages[len(fd.Messages)-1].Name
				}
				fd.Messages = append(fd.Messages, &msgDesc{Name: name, Fields: []string{"a", "b"}})
			}
			for i := 0; i < r.Intn(3); i++ {
				fd.Enums = append(fd.Enums, &enumDesc{Name: fmt.Sprintf("E%d", r.Intn(4))})
			}
			confSet := map[string]struct{}{}
			seen := map[string]struct{}{}
			if !paths[path] {
				if r.Intn(10) == 0 { // 偶尔注入非法项
					fd.Package = ""
				}
				for _, mm := range fd.Messages {
					if fd.Package == "" {
						break
					}
					full := fullName(fd.Package, mm.Name)
					if _, dup := seen[full]; dup {
						confSet[full] = struct{}{}
					}
					seen[full] = struct{}{}
					if _, exists := model[full]; exists {
						confSet[full] = struct{}{}
					}
				}
				for _, ee := range fd.Enums {
					if fd.Package == "" {
						break
					}
					full := fullName(fd.Package, ee.Name)
					if _, dup := seen[full]; dup {
						confSet[full] = struct{}{}
					}
					seen[full] = struct{}{}
					if _, exists := model[full]; exists {
						confSet[full] = struct{}{}
					}
				}
			}
			err := rr.Register(fd)
			switch {
			case fd.Package == "":
				ckSeed(errors.Is(err, errInvalid), "random invalid rejected")
			case paths[path]:
				ckSeed(errors.Is(err, errDupFile), "random duplicate file rejected")
			case len(confSet) > 0:
				if !errors.Is(err, errConflict) {
					ckSeed(false, "random conflict rejected")
				} else if ce, ok := err.(*conflictError); ok {
					want := make([]string, 0, len(confSet))
					for n := range confSet {
						want = append(want, n)
					}
					sort.Strings(want)
					if len(ce.Names) != len(want) {
						ckSeed(false, "random conflict names count")
					} else {
						for i := range want {
							if ce.Names[i] != want[i] {
								ckSeed(false, "random conflict names sorted")
								break
							}
						}
					}
				} else {
					ckSeed(false, "random conflict error type")
				}
			default:
				ckSeed(err == nil, "random registration succeeds")
				paths[path] = true
				for _, mm := range fd.Messages {
					model[fullName(fd.Package, mm.Name)] = "message"
				}
				for _, ee := range fd.Enums {
					model[fullName(fd.Package, ee.Name)] = "enum"
				}
			}
			if len(rr.bySymbol) != len(model) {
				ckSeed(false, "random registry symbol count")
			}
			if len(rr.files) != len(paths) {
				ckSeed(false, "random registry file count")
			}
		}
		if round%11 == 0 {
			gcStress()
			keep = append(keep, held{reg: rr, msgs: len(rr.msgs), syms: len(rr.bySymbol)})
		}
	}
	gcSink = append(gcSink, []byte("random-registry"))
	runtime.GC()
	for i := range keep {
		h := &keep[i]
		ckSeed(len(h.reg.msgs) == h.msgs && len(h.reg.bySymbol) == h.syms, "retained registry stable after GC")
		ckSeed(len(h.reg.Symbols()) == h.syms, "retained symbol listing stable after GC")
	}

	finish("l10_registry_dup_detect")
}
