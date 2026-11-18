// L10: 生成风格 init 注册表：按类型注册构造函数、保持声明顺序、重复/非法/冻结检测。
// 附带 protoc-gen-go 形态的 file descriptor sync.Once 幂等初始化。
package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	ErrDuplicate = errors.New("registry: duplicate type")
	ErrInvalid   = errors.New("registry: invalid registration")
	ErrFrozen    = errors.New("registry: frozen")
	ErrUnknown   = errors.New("registry: unknown type")
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

// 以下类型模拟不同 .proto 消息的生成物。
type entity interface {
	Kind() string
	Fields() []string
	Clone() entity
}

type user struct {
	Name   string
	Email  string
	Active bool
}
type post struct {
	Title string
	Body  string
	Tags  []string
}
type comment struct {
	PostID uint64
	Text   string
}
type attachment struct {
	File string
	Size int64
}
type session struct {
	Token string
	TTL   int
}

type factory func() entity

func (x *user) Kind() string       { return "user" }
func (x *post) Kind() string       { return "post" }
func (x *comment) Kind() string    { return "comment" }
func (x *attachment) Kind() string { return "attachment" }
func (x *session) Kind() string    { return "session" }

func (x *user) Fields() []string       { return []string{"name", "email", "active"} }
func (x *post) Fields() []string       { return []string{"title", "body", "tags"} }
func (x *comment) Fields() []string    { return []string{"post_id", "text"} }
func (x *attachment) Fields() []string { return []string{"file", "size"} }
func (x *session) Fields() []string    { return []string{"token", "ttl"} }

func (x *user) Clone() entity {
	c := *x
	return &c
}
func (x *post) Clone() entity {
	c := *x
	c.Tags = append([]string(nil), x.Tags...)
	return &c
}
func (x *comment) Clone() entity    { c := *x; return &c }
func (x *attachment) Clone() entity { c := *x; return &c }
func (x *session) Clone() entity    { c := *x; return &c }

// registry 模拟 protoc-gen-go 的全局 registration 表。
type registry struct {
	order     []string
	factories map[string]factory
	frozen    bool
}

func newRegistry() *registry {
	return &registry{factories: make(map[string]factory)}
}

func (r *registry) register(kind string, f factory) error {
	if kind == "" || f == nil {
		return ErrInvalid
	}
	if r.frozen {
		return ErrFrozen
	}
	if _, ok := r.factories[kind]; ok {
		return ErrDuplicate
	}
	r.factories[kind] = f
	r.order = append(r.order, kind)
	return nil
}

func (r *registry) freeze() {
	r.frozen = true
}

func (r *registry) lookup(kind string) (entity, error) {
	f, ok := r.factories[kind]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknown, kind)
	}
	return f(), nil
}

func (r *registry) snapshot() []string {
	return append([]string(nil), r.order...)
}

var globalRegistry = newRegistry()

func mustRegister(kind string, f factory) {
	if err := globalRegistry.register(kind, f); err != nil {
		panic(fmt.Sprintf("init registration %q: %v", kind, err))
	}
}

// 生成风格 init：每个 init 块对应生成文件里的一个 var/init 段。
func init() {
	mustRegister("user", func() entity {
		return &user{Name: "anonymous", Email: "anon@example.com", Active: true}
	})
}

func init() {
	mustRegister("post", func() entity {
		return &post{Title: "untitled", Body: "", Tags: []string{"draft"}}
	})
}

func init() {
	mustRegister("comment", func() entity { return &comment{PostID: 0, Text: "hi"} })
}

func init() {
	mustRegister("attachment", func() entity {
		return &attachment{File: "a.bin", Size: 1024}
	})
}

func init() {
	mustRegister("session", func() entity { return &session{Token: "tok", TTL: 60} })
}

// file descriptor 初始化：protoc-gen-go 生成物使用 sync.Once + 包级指针。
type fileDesc struct {
	Name     string
	Messages []string
	encoded  []byte
}

var (
	fileOrderOnce sync.Once
	fileOrderPtr  *fileDesc
)

func fileOrderProtoInit() *fileDesc {
	fileOrderOnce.Do(func() {
		fileOrderPtr = &fileDesc{
			Name:     "order.proto",
			Messages: []string{"user", "post", "comment"},
			encoded:  []byte{0x0a, 0x07, 'o', 'r', 'd', 'e', 'r'},
		}
	})
	return fileOrderPtr
}

func init() {
	// 生成文件中常见的初始化调用：多个 init 重复调用也只执行一次。
	_ = fileOrderProtoInit()
	_ = fileOrderProtoInit()
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	wantOrder := []string{"user", "post", "comment", "attachment", "session"}
	order := globalRegistry.snapshot()
	ck(reflect.DeepEqual(order, wantOrder), "init registration order")
	ck(len(order) == 5, "five registered types")

	// 1. 每个注册项都能构造出正确类型与字段表。
	for _, kind := range wantOrder {
		e, err := globalRegistry.lookup(kind)
		ck(err == nil, "lookup "+kind)
		if err != nil {
			continue
		}
		ck(e.Kind() == kind, "kind "+kind)
		ck(len(e.Fields()) > 0, "fields "+kind)
		c := e.Clone()
		ck(c != e, "clone distinct "+kind)
		ck(c.Kind() == e.Kind() && reflect.DeepEqual(c.Fields(), e.Fields()), "clone equal "+kind)
		gcSink = append(gcSink, []byte(kind))
	}

	// 2. 重复注册：返回 ErrDuplicate，顺序与首个工厂不变。
	err := globalRegistry.register("user", func() entity { return &user{Name: "replaced"} })
	ck(errors.Is(err, ErrDuplicate), "duplicate rejected")
	ck(reflect.DeepEqual(globalRegistry.snapshot(), wantOrder), "order unchanged after duplicate")
	e, _ := globalRegistry.lookup("user")
	u, ok := e.(*user)
	ck(ok && u.Name == "anonymous", "first registration wins")

	// 3. 非法注册：空类型名、nil 工厂。
	ck(errors.Is(globalRegistry.register("", func() entity { return &user{} }), ErrInvalid), "empty kind rejected")
	ck(errors.Is(globalRegistry.register("ghost", nil), ErrInvalid), "nil factory rejected")
	ck(!reflect.DeepEqual(globalRegistry.snapshot(), append(wantOrder, "ghost")), "invalid not registered")

	// 4. 未知查询。
	_, err = globalRegistry.lookup("missing")
	ck(errors.Is(err, ErrUnknown), "unknown lookup")

	// 5. 冻结：新注册被拒绝，已有注册仍可用。
	globalRegistry.freeze()
	ck(errors.Is(globalRegistry.register("late", func() entity { return &session{} }), ErrFrozen), "frozen rejects new")
	globalRegistry.freeze()
	ck(reflect.DeepEqual(globalRegistry.snapshot(), wantOrder), "frozen order stable")

	// 6. GC 后注册表、工厂闭包、entity 指针全部存活。
	var held []entity
	for i := 0; i < 200; i++ {
		e, err := globalRegistry.lookup(wantOrder[i%len(wantOrder)])
		ckSeed(err == nil, "lookup before GC")
		if err == nil {
			held = append(held, e)
		}
	}
	gcStress()
	ck(reflect.DeepEqual(globalRegistry.snapshot(), wantOrder), "order after GC")
	for i, e := range held {
		if e == nil || e.Kind() != wantOrder[i%len(wantOrder)] || len(e.Clone().Fields()) == 0 {
			ckSeed(false, fmt.Sprintf("held entity %d after GC", i))
			break
		}
	}

	// 7. file descriptor init 幂等：多次调用返回同一指针，GC 后仍可用。
	a1 := fileOrderProtoInit()
	a2 := fileOrderProtoInit()
	ck(a1 == a2, "file init pointer stable")
	ck(a1.Name == "order.proto" && reflect.DeepEqual(a1.Messages, []string{"user", "post", "comment"}), "file desc content")
	gcSink = append(gcSink, a1.encoded)
	gcStress()
	ck(fileOrderProtoInit() == a1, "file init stable after GC")
	ck(len(a1.encoded) == 7 && a1.encoded[2] == 'o', "file desc bytes survive GC")

	finish("l10_init_registry")
}
