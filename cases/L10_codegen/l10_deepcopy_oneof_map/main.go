// L10: 生成风格 DeepCopy 变体：oneof 分支、map[string][]byte、指针切片、
// optional 标量与「复用目标对象」的 DeepCopyInto 语义。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"sort"
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

// ---- 生成风格消息 ----

type child struct {
	Name string
	Tags []string
	Data []byte
}

func (c *child) DeepCopyInto(out *child) {
	*out = *c
	if c.Tags != nil {
		out.Tags = append([]string{}, c.Tags...)
	}
	if c.Data != nil {
		out.Data = make([]byte, len(c.Data))
		copy(out.Data, c.Data)
	}
}

func (c *child) DeepCopy() *child {
	if c == nil {
		return nil
	}
	out := new(child)
	c.DeepCopyInto(out)
	return out
}

type choice interface {
	choiceKind() string
}

type textChoice struct{ Text string }
type blobChoice struct{ Blob []byte }
type childChoice struct{ Child *child }
type listChoice struct{ List []int32 }

func (*textChoice) choiceKind() string  { return "text" }
func (*blobChoice) choiceKind() string  { return "blob" }
func (*childChoice) choiceKind() string { return "child" }
func (*listChoice) choiceKind() string  { return "list" }

type msg struct {
	ID     int64
	Choice choice
	Attrs  map[string][]byte
	Items  []*child
	Opt    *int32
	Note   string
}

func cloneChoice(c choice) choice {
	if c == nil {
		return nil
	}
	switch v := c.(type) {
	case *textChoice:
		if v == nil {
			return v
		}
		return &textChoice{Text: v.Text}
	case *blobChoice:
		if v == nil {
			return v
		}
		out := &blobChoice{}
		if v.Blob != nil {
			out.Blob = make([]byte, len(v.Blob))
			copy(out.Blob, v.Blob)
		}
		return out
	case *childChoice:
		if v == nil {
			return v
		}
		return &childChoice{Child: v.Child.DeepCopy()}
	case *listChoice:
		if v == nil {
			return v
		}
		out := &listChoice{}
		if v.List != nil {
			out.List = make([]int32, len(v.List))
			copy(out.List, v.List)
		}
		return out
	default:
		return c
	}
}

func (m *msg) DeepCopyInto(out *msg) {
	*out = *m
	out.Choice = cloneChoice(m.Choice)
	if m.Attrs == nil {
		out.Attrs = nil
	} else {
		out.Attrs = make(map[string][]byte, len(m.Attrs))
		for k, v := range m.Attrs {
			if v == nil {
				out.Attrs[k] = nil
				continue
			}
			nv := make([]byte, len(v))
			copy(nv, v)
			out.Attrs[k] = nv
		}
	}
	if m.Items == nil {
		out.Items = nil
	} else {
		out.Items = make([]*child, len(m.Items))
		for i, it := range m.Items {
			out.Items[i] = it.DeepCopy()
		}
	}
	if m.Opt == nil {
		out.Opt = nil
	} else {
		v := *m.Opt
		out.Opt = &v
	}
}

func (m *msg) DeepCopy() *msg {
	if m == nil {
		return nil
	}
	out := new(msg)
	m.DeepCopyInto(out)
	return out
}

// 生成风格 oneof 访问器。
func (m *msg) HasChoice() bool {
	if m == nil {
		return false
	}
	return m.Choice != nil
}

func (m *msg) WhichChoice() string {
	if m == nil || m.Choice == nil {
		return ""
	}
	return m.Choice.choiceKind()
}

func (m *msg) GetText() string {
	if m == nil {
		return ""
	}
	c, ok := m.Choice.(*textChoice)
	if !ok || c == nil {
		return ""
	}
	return c.Text
}

func (m *msg) GetBlob() []byte {
	if m == nil {
		return nil
	}
	c, ok := m.Choice.(*blobChoice)
	if !ok || c == nil {
		return nil
	}
	return c.Blob
}

func (m *msg) GetChild() *child {
	if m == nil {
		return nil
	}
	c, ok := m.Choice.(*childChoice)
	if !ok || c == nil {
		return nil
	}
	return c.Child
}

func (m *msg) GetList() []int32 {
	if m == nil {
		return nil
	}
	c, ok := m.Choice.(*listChoice)
	if !ok || c == nil {
		return nil
	}
	return c.List
}

func (m *msg) SetText(s string)   { m.Choice = &textChoice{Text: s} }
func (m *msg) SetBlob(b []byte)   { m.Choice = &blobChoice{Blob: b} }
func (m *msg) SetChild(c *child)  { m.Choice = &childChoice{Child: c} }
func (m *msg) SetList(v []int32)  { m.Choice = &listChoice{List: v} }
func (m *msg) ClearChoice()       { m.Choice = nil }

func choiceDump(c choice) string {
	if c == nil {
		return "none"
	}
	switch v := c.(type) {
	case *textChoice:
		if v == nil {
			return "text(nil)"
		}
		return "text:" + v.Text
	case *blobChoice:
		if v == nil {
			return "blob(nil)"
		}
		return fmt.Sprintf("blob:%x", v.Blob)
	case *childChoice:
		if v == nil {
			return "child(nil)"
		}
		if v.Child == nil {
			return "child<ptr-nil>"
		}
		return "child:" + childDump(v.Child)
	case *listChoice:
		if v == nil {
			return "list(nil)"
		}
		return fmt.Sprintf("list:%v", v.List)
	default:
		return fmt.Sprintf("unknown:%T", c)
	}
}

func childDump(c *child) string {
	if c == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s|%v|%x", c.Name, c.Tags, c.Data)
}

func dump(m *msg) string {
	if m == nil {
		return "<nil>"
	}
	s := fmt.Sprintf("id=%d;choice=%s;note=%s;opt=", m.ID, choiceDump(m.Choice), m.Note)
	if m.Opt == nil {
		s += "<nil>"
	} else {
		s += strconv.FormatInt(int64(*m.Opt), 10)
	}
	s += ";attrs:"
	if m.Attrs == nil {
		s += "<nil>"
	} else {
		keys := make([]string, 0, len(m.Attrs))
		for k := range m.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := m.Attrs[k]
			if v == nil {
				s += "|" + k + "=<nil>"
			} else {
				s += fmt.Sprintf("|%s=%x", k, v)
			}
		}
	}
	s += ";items:"
	if m.Items == nil {
		s += "<nil>"
	} else {
		for _, it := range m.Items {
			s += "|" + childDump(it)
		}
	}
	return s
}

func sliceShared(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return &a[0] == &b[0]
}

func childNoShared(a, b *child) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b {
		return false
	}
	if sliceShared(a.Data, b.Data) {
		return false
	}
	if len(a.Tags) > 0 && len(b.Tags) > 0 && &a.Tags[0] == &b.Tags[0] {
		return false
	}
	return true
}

func noShared(a, b *msg) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b {
		return false
	}
	if a.Opt != nil && b.Opt != nil && a.Opt == b.Opt {
		return false
	}
	switch ca := a.Choice.(type) {
	case *blobChoice:
		if cb, ok := b.Choice.(*blobChoice); ok {
			if ca != nil && cb != nil && sliceShared(ca.Blob, cb.Blob) {
				return false
			}
		}
	case *listChoice:
		if cb, ok := b.Choice.(*listChoice); ok {
			if ca != nil && cb != nil && len(ca.List) > 0 && len(cb.List) > 0 && &ca.List[0] == &cb.List[0] {
				return false
			}
		}
	case *childChoice:
		if cb, ok := b.Choice.(*childChoice); ok {
			if ca != nil && cb != nil {
				if ca.Child != nil && cb.Child != nil && !childNoShared(ca.Child, cb.Child) {
					return false
				}
			}
		}
	}
	if a.Attrs != nil && b.Attrs != nil {
		if reflect.ValueOf(a.Attrs).Pointer() == reflect.ValueOf(b.Attrs).Pointer() {
			return false
		}
		for k, av := range a.Attrs {
			if bv, ok := b.Attrs[k]; ok && sliceShared(av, bv) {
				return false
			}
		}
	}
	if len(a.Items) != len(b.Items) {
		return false
	}
	for i := range a.Items {
		if !childNoShared(a.Items[i], b.Items[i]) {
			return false
		}
	}
	return true
}

func int32p(v int32) *int32 { return &v }

func randChild(r *rand.Rand) *child {
	tags := make([]string, r.Intn(3))
	for i := range tags {
		tags[i] = fmt.Sprintf("t%d", r.Intn(100))
	}
	data := make([]byte, r.Intn(10))
	for i := range data {
		data[i] = byte(r.Intn(256))
	}
	return &child{Name: fmt.Sprintf("c%d", r.Intn(1000)), Tags: tags, Data: data}
}

func randChoice(r *rand.Rand) choice {
	switch r.Intn(7) {
	case 0:
		return nil
	case 1:
		return &textChoice{Text: fmt.Sprintf("s%d", r.Intn(1000))}
	case 2:
		b := make([]byte, r.Intn(12))
		for i := range b {
			b[i] = byte(r.Intn(256))
		}
		return &blobChoice{Blob: b}
	case 3:
		return &childChoice{Child: randChild(r)}
	case 4:
		return &childChoice{Child: nil}
	case 5:
		var c *childChoice
		return c
	case 6:
		v := make([]int32, r.Intn(5))
		for i := range v {
			v[i] = int32(r.Intn(2000) - 1000)
		}
		return &listChoice{List: v}
	default:
		return &textChoice{Text: ""}
	}
}

func randMsg(r *rand.Rand) *msg {
	m := &msg{ID: r.Int63n(1 << 40), Choice: randChoice(r), Note: fmt.Sprintf("n%d", r.Intn(1000))}
	if r.Intn(3) != 0 {
		m.Attrs = make(map[string][]byte)
		for i := 0; i < r.Intn(4); i++ {
			k := fmt.Sprintf("k%d", r.Intn(8))
			switch r.Intn(4) {
			case 0:
				m.Attrs[k] = nil
			case 1:
				m.Attrs[k] = []byte{}
			default:
				b := make([]byte, r.Intn(8)+1)
				for j := range b {
					b[j] = byte(r.Intn(256))
				}
				m.Attrs[k] = b
			}
		}
	}
	if r.Intn(3) != 0 {
		n := r.Intn(4)
		m.Items = make([]*child, n)
		for i := range m.Items {
			if r.Intn(5) != 0 {
				m.Items[i] = randChild(r)
			}
		}
	}
	if r.Intn(3) == 0 {
		m.Opt = int32p(int32(r.Intn(3) - 1))
	}
	return m
}

func mutate(m *msg, r *rand.Rand) {
	m.Note += "!"
	m.ID++
	if m.Opt != nil {
		*m.Opt++
	}
	if b := m.GetBlob(); len(b) > 0 {
		b[0] ^= 0x5a
	}
	if l := m.GetList(); len(l) > 0 {
		l[0] += 1000
	}
	if c := m.GetChild(); c != nil {
		c.Name += "!"
		if len(c.Data) > 0 {
			c.Data[0] ^= 0xa5
		}
		if len(c.Tags) > 0 {
			c.Tags[0] += "!"
		}
	}
	for k, v := range m.Attrs {
		if len(v) > 0 {
			v[0] ^= 0x33
			break
		}
		_ = k
	}
	for _, it := range m.Items {
		if it != nil && len(it.Data) > 0 {
			it.Data[len(it.Data)-1] ^= 0x11
			break
		}
	}
	_ = r
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 固定消息：各层深拷贝、别名关系、nil/空语义。
	shared := &child{Name: "shared", Tags: []string{"a"}, Data: []byte{1, 2}}
	orig := &msg{
		ID:     42,
		Choice: &blobChoice{Blob: []byte{9, 8, 7}},
		Attrs:  map[string][]byte{"a": {1, 2}, "b": nil, "c": {}, "d": {3}},
		Items:  []*child{shared, nil, {Name: "x", Data: []byte{4}}},
		Opt:    int32p(0),
		Note:   "orig",
	}
	cp := orig.DeepCopy()
	ck(dump(cp) == dump(orig), "fixed deep copy dump equal")
	ck(noShared(orig, cp), "fixed deep copy no shared refs")
	ck(cp != orig && cp.Items[0] != shared && cp.Items[1] == nil && cp.Items[2] != orig.Items[2],
		"items duplicated, nil element preserved")
	ck(cp.Opt != nil && *cp.Opt == 0 && cp.Opt != orig.Opt, "explicit zero optional duplicated with presence")
	ck(cp.Attrs["b"] == nil, "nil map value stays nil")
	ck(cp.Attrs["c"] != nil && len(cp.Attrs["c"]) == 0, "empty map value stays non-nil empty")
	ck(sliceShared(cp.Choice.(*blobChoice).Blob, orig.Choice.(*blobChoice).Blob) == false,
		"oneof blob copied")
	ck(cp.WhichChoice() == "blob" && len(cp.GetBlob()) == 3, "choice accessors")

	// 2. typed-nil oneof 与访问器语义。
	tn := &msg{Choice: (*childChoice)(nil)}
	tnc := tn.DeepCopy()
	ck(tnc.Choice != nil && tnc.GetChild() == nil && tnc.WhichChoice() == "child" && tnc.HasChoice(),
		"typed-nil child choice preserved as typed nil")
	ck(dump(tnc) == dump(tn), "typed-nil choice dump equal")
	ck((&msg{}).WhichChoice() == "" && !(&msg{}).HasChoice(), "unset choice")
	var nilMsg *msg
	ck(nilMsg.DeepCopy() == nil, "nil receiver deep copy")
	ck(nilMsg.GetText() == "" && nilMsg.GetBlob() == nil && nilMsg.GetChild() == nil && nilMsg.GetList() == nil,
		"nil receiver choice getters")
	nilChoiceClone := (&msg{Choice: nil}).DeepCopy()
	ck(nilChoiceClone.Choice == nil, "nil choice stays nil")

	// 3. 空/nil 容器语义。
	e := &msg{Attrs: map[string][]byte{}, Items: []*child{}, Opt: nil}
	ec := e.DeepCopy()
	ck(ec.Attrs != nil && len(ec.Attrs) == 0, "empty attrs preserved non-nil")
	ck(ec.Items != nil && len(ec.Items) == 0, "empty items preserved non-nil")
	ck(ec.Opt == nil, "nil optional stays nil")
	n := (&msg{}).DeepCopy()
	ck(n.Attrs == nil && n.Items == nil && n.Opt == nil, "nil containers preserved")

	// 4. 复用已脏的目标：覆盖而非追加/合并，旧载荷不再被引用。
	dst := &msg{
		Choice: &blobChoice{Blob: make([]byte, 100)},
		Attrs:  map[string][]byte{"old1": {1}, "old2": {2}, "a": {9, 9, 9}},
		Items:  []*child{{Name: "oldA"}, {Name: "oldB"}, {Name: "oldC"}},
		Opt:    int32p(77),
		Note:   "dirty",
	}
	oldBlob := dst.Choice.(*blobChoice).Blob
	oldKid := dst.Items[0]
	orig.DeepCopyInto(dst)
	ck(dump(dst) == dump(orig), "DeepCopyInto overwrites dirty destination")
	ck(len(dst.Items) == len(orig.Items), "destination items replaced not appended")
	ck(len(dst.Attrs) == len(orig.Attrs) && dst.Attrs["old1"] == nil && dst.Attrs["old2"] == nil,
		"destination attrs replaced not merged")
	ck(dst.Items[0] != oldKid && oldKid.Name == "oldA", "old destination child not referenced")
	oldBlob[0] = 0xee
	ck(dump(dst) == dump(orig), "old destination backing not referenced")

	// 5. GC：源/副本/访问器视图在 GC 后一致。
	wantOrig := dump(orig)
	wantCopy := dump(cp)
	blobView := orig.GetBlob()
	attrView := orig.Attrs["a"]
	childView := orig.Items[2]
	gcSink = append(gcSink, blobView, attrView, childView.Data)
	gcStress()
	ck(dump(orig) == wantOrig, "source survives GC")
	ck(dump(cp) == wantCopy, "copy survives GC")
	ck(len(blobView) == 3 && blobView[2] == 7, "oneof blob view survives GC")
	ck(attrView[0] == 1 && childView.Data[0] == 4, "map/child views survive GC")

	// 6. 双向修改隔离。
	cpSnap := dump(cp)
	orig.Note += "-mut"
	orig.ID++
	*orig.Opt = 99
	orig.GetBlob()[0] = 0xaa
	orig.Attrs["a"][0] = 0xbb
	orig.Attrs["new"] = []byte{1}
	orig.Items[0].Name += "-mut"
	orig.Items[0].Data[0] = 0xcc
	ck(dump(cp) == cpSnap, "copy unaffected by source mutation")
	origSnap := dump(orig)
	cp.Items[2].Name += "-cp"
	cp.GetBlob()[1] = 0xdd
	cp.Attrs["a"][0] = 0xee
	ck(dump(orig) == origSnap, "source unaffected by copy mutation")

	// 7. UAF_SEED 随机消息：拷贝相等、无共享、源修改隔离、GC、二次拷贝、复用目标。
	r := rand.New(rand.NewSource(curSeed))
	for i := 0; i < 160; i++ {
		src := randMsg(r)
		dst := src.DeepCopy()
		ckSeed(dump(dst) == dump(src), fmt.Sprintf("round %d copy equal", i))
		ckSeed(noShared(src, dst), fmt.Sprintf("round %d no shared refs", i))
		snap := dump(dst)
		mutate(src, r)
		ckSeed(dump(dst) == snap, fmt.Sprintf("round %d isolated from source mutation", i))
		again := dst.DeepCopy()
		ckSeed(dump(again) == dump(dst), fmt.Sprintf("round %d second copy equal", i))
		if i%23 == 0 {
			gcStress()
			ckSeed(dump(dst) == snap, fmt.Sprintf("round %d copy survives GC", i))
			ckSeed(dump(again) == snap, fmt.Sprintf("round %d second copy survives GC", i))
		}
		if i%31 == 0 {
			dirty := randMsg(r)
			src.DeepCopyInto(dirty)
			ckSeed(dump(dirty) == dump(src), fmt.Sprintf("round %d DeepCopyInto dirty destination", i))
			ckSeed(noShared(src, dirty), fmt.Sprintf("round %d reused destination no sharing", i))
		}
	}

	finish("l10_deepcopy_oneof_map")
}
