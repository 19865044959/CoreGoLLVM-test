// L10: 生成风格 getter/setter 方法集（opaque 访问器形态）：指针字段 presence、GC 后访问、nil receiver。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
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

// 生成风格 opaque 消息：字段不可导出，只能通过访问器读写。
type profile struct {
	Nick  string
	Level int32
	Blob  []byte
}

type peer struct {
	Addr string
	RTT  int64
}

type account struct {
	id      *int64
	name    *string
	email   *string
	age     *int32
	score   *float64
	active  *bool
	tags    []string
	profile *profile
	attrs   map[string]string
	peers   []*peer
	state   uint32
}

func (a *account) GetID() int64 {
	if a != nil && a.id != nil {
		return *a.id
	}
	return 0
}
func (a *account) SetID(v int64) { a.id = &v }
func (a *account) HasID() bool   { return a != nil && a.id != nil }
func (a *account) ClearID()      { a.id = nil }

func (a *account) GetName() string {
	if a != nil && a.name != nil {
		return *a.name
	}
	return ""
}
func (a *account) SetName(v string) { a.name = &v }
func (a *account) HasName() bool    { return a != nil && a.name != nil }
func (a *account) ClearName()       { a.name = nil }

func (a *account) GetEmail() string {
	if a != nil && a.email != nil {
		return *a.email
	}
	return ""
}
func (a *account) SetEmail(v string) { a.email = &v }
func (a *account) HasEmail() bool    { return a != nil && a.email != nil }
func (a *account) ClearEmail()       { a.email = nil }

func (a *account) GetAge() int32 {
	if a != nil && a.age != nil {
		return *a.age
	}
	return 0
}
func (a *account) SetAge(v int32) { a.age = &v }
func (a *account) HasAge() bool   { return a != nil && a.age != nil }
func (a *account) ClearAge()      { a.age = nil }

func (a *account) GetScore() float64 {
	if a != nil && a.score != nil {
		return *a.score
	}
	return 0
}
func (a *account) SetScore(v float64) { a.score = &v }
func (a *account) HasScore() bool     { return a != nil && a.score != nil }
func (a *account) ClearScore()        { a.score = nil }

func (a *account) GetActive() bool {
	if a != nil && a.active != nil {
		return *a.active
	}
	return false
}
func (a *account) SetActive(v bool) { a.active = &v }
func (a *account) HasActive() bool  { return a != nil && a.active != nil }
func (a *account) ClearActive()     { a.active = nil }

func (a *account) GetTags() []string {
	if a == nil {
		return nil
	}
	return a.tags
}
func (a *account) SetTags(v []string) { a.tags = v }
func (a *account) AddTag(v string)    { a.tags = append(a.tags, v) }
func (a *account) ClearTags()         { a.tags = nil }

func (a *account) GetProfile() *profile {
	if a == nil {
		return nil
	}
	return a.profile
}
func (a *account) SetProfile(v *profile) { a.profile = v }
func (a *account) HasProfile() bool      { return a != nil && a.profile != nil }
func (a *account) ClearProfile()         { a.profile = nil }

func (a *account) GetAttrs() map[string]string {
	if a == nil {
		return nil
	}
	return a.attrs
}
func (a *account) SetAttrs(v map[string]string) { a.attrs = v }
func (a *account) GetAttr(k string) string {
	if a == nil || a.attrs == nil {
		return ""
	}
	return a.attrs[k]
}

func (a *account) GetPeers() []*peer {
	if a == nil {
		return nil
	}
	return a.peers
}
func (a *account) SetPeers(v []*peer) { a.peers = v }
func (a *account) GetState() uint32   { return a.state }
func (a *account) SetState(v uint32)  { a.state = v }

// Reset 与生成代码一致：清空业务字段，可用于复用。
func (a *account) Reset() {
	*a = account{}
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 零值默认与 presence。
	var a account
	ck(a.GetID() == 0 && a.GetName() == "" && a.GetAge() == 0 && !a.GetActive(), "zero getters")
	ck(!a.HasID() && !a.HasName() && !a.HasAge() && !a.HasActive() && !a.HasProfile(), "zero presence")
	ck(a.GetTags() == nil && a.GetProfile() == nil && a.GetAttrs() == nil && a.GetPeers() == nil, "zero containers")

	a.SetID(0)
	a.SetName("")
	a.SetActive(false)
	ck(a.HasID() && a.HasName() && a.HasActive(), "explicit zero presence")
	ck(a.GetID() == 0 && a.GetName() == "" && !a.GetActive(), "explicit zero values")

	// 2. 赋值 → GC → 读取，指针字段与容器载荷必须存活。
	a.SetID(-42)
	a.SetName("alice")
	a.SetEmail("alice@example.com")
	a.SetAge(33)
	a.SetScore(12.5)
	a.SetActive(true)
	a.SetTags([]string{"red", "green"})
	prof := &profile{Nick: "ally", Level: 9, Blob: []byte{1, 2, 3}}
	a.SetProfile(prof)
	a.SetAttrs(map[string]string{"team": "core", "zone": "z1"})
	p1 := &peer{Addr: "10.0.0.1", RTT: 3}
	p2 := &peer{Addr: "10.0.0.2", RTT: 7}
	a.SetPeers([]*peer{p1, p2})
	a.SetState(3)

	gcSink = append(gcSink, prof.Blob)
	gcStress()

	ck(a.GetID() == -42 && a.GetName() == "alice" && a.GetEmail() == "alice@example.com", "scalar getters after GC")
	ck(a.GetAge() == 33 && a.GetScore() == 12.5 && a.GetActive(), "numeric getters after GC")
	ck(reflect.DeepEqual(a.GetTags(), []string{"red", "green"}), "tags survive GC")
	ck(a.GetProfile().Nick == "ally" && a.GetProfile().Level == 9 && reflect.DeepEqual(a.GetProfile().Blob, []byte{1, 2, 3}), "profile survives GC")
	ck(a.GetAttr("team") == "core" && a.GetAttr("zone") == "z1" && a.GetAttr("missing") == "", "attrs survive GC")
	ck(len(a.GetPeers()) == 2 && a.GetPeers()[0].Addr == "10.0.0.1" && a.GetPeers()[1].RTT == 7, "peers survive GC")
	ck(a.GetState() == 3, "state")

	// 3. 循环 Set 产生独立指针（不共享循环变量地址）。
	var ids []*int64
	for i := int64(0); i < 8; i++ {
		a.SetID(i)
		if a.id != nil {
			ids = append(ids, a.id)
		}
	}
	distinct := true
	seen := map[*int64]bool{}
	for _, p := range ids {
		if seen[p] {
			distinct = false
		}
		seen[p] = true
	}
	ck(distinct, "setters allocate distinct pointers")
	gcStress()
	last := int64(0)
	for i, p := range ids {
		if p == nil || *p != int64(i) {
			last++
		}
	}
	ck(last == 0, "pointer fields survive GC after repeated set")

	// 4. nil receiver：getter/Has 安全，setter panic。
	var nilAcc *account
	ck(nilAcc.GetID() == 0 && nilAcc.GetName() == "" && !nilAcc.HasID(), "nil receiver getters")
	ck(nilAcc.GetTags() == nil && nilAcc.GetProfile() == nil, "nil receiver containers")
	func() {
		defer func() {
			ck(recover() != nil, "nil receiver setter panics")
		}()
		nilAcc.SetID(1)
	}()

	// 5. 随机字段组合 × UAF_SEED，GC 穿插后逐字段核对。
	r := rand.New(rand.NewSource(curSeed))
	for iter := 0; iter < 200; iter++ {
		var acc account
		var expID int64
		var expName string
		var expAge int32
		var expActive bool
		var expTags []string
		var expProf *profile
		var expAttrs map[string]string
		setID := r.Intn(2) == 0
		setName := r.Intn(2) == 0
		setAge := r.Intn(2) == 0
		setActive := r.Intn(2) == 0
		if setID {
			expID = r.Int63n(1<<40) - (1 << 39)
			acc.SetID(expID)
		}
		if setName {
			expName = fmt.Sprintf("user-%d", r.Intn(10000))
			acc.SetName(expName)
		}
		if setAge {
			expAge = int32(r.Intn(120) - 20)
			acc.SetAge(expAge)
		}
		if setActive {
			expActive = r.Intn(2) == 0
			acc.SetActive(expActive)
		}
		if r.Intn(2) == 0 {
			expTags = []string{fmt.Sprintf("t%d", r.Intn(100)), fmt.Sprintf("t%d", r.Intn(100))}
			acc.SetTags(expTags)
		}
		if r.Intn(3) == 0 {
			expProf = &profile{Nick: fmt.Sprintf("p%d", r.Intn(1000)), Level: int32(r.Intn(10)), Blob: make([]byte, r.Intn(16))}
			acc.SetProfile(expProf)
		}
		if r.Intn(2) == 0 {
			expAttrs = map[string]string{"k": fmt.Sprintf("v%d", r.Intn(100))}
			acc.SetAttrs(expAttrs)
		}
		if iter%13 == 0 {
			gcStress()
		}
		ckSeed(acc.HasID() == setID && acc.GetID() == expID, "random ID accessor")
		ckSeed(acc.HasName() == setName && acc.GetName() == expName, "random name accessor")
		ckSeed(acc.HasAge() == setAge && acc.GetAge() == expAge, "random age accessor")
		ckSeed(acc.HasActive() == setActive && acc.GetActive() == expActive, "random active accessor")
		ckSeed(reflect.DeepEqual(acc.GetTags(), expTags), "random tags accessor")
		ckSeed(reflect.DeepEqual(acc.GetProfile(), expProf), "random profile accessor")
		ckSeed(reflect.DeepEqual(acc.GetAttrs(), expAttrs), "random attrs accessor")
		// Clear 后回到默认值并保持 zero presence。
		acc.ClearID()
		acc.ClearName()
		acc.ClearAge()
		acc.ClearActive()
		acc.ClearTags()
		acc.ClearProfile()
		ckSeed(!acc.HasID() && !acc.HasName() && acc.GetID() == 0 && acc.GetName() == "", "clear scalar")
		ckSeed(acc.GetTags() == nil && acc.GetProfile() == nil, "clear containers")
	}

	// 6. Reset 后对象可复用。
	a.Reset()
	ck(!a.HasID() && !a.HasProfile() && a.GetTags() == nil && a.GetPeers() == nil && a.GetState() == 0, "reset clears all")
	a.SetName("again")
	gcStress()
	ck(a.GetName() == "again" && !a.HasID(), "reuse after reset")

	finish("l10_generated_getters")
}
