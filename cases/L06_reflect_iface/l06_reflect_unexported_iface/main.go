// L06: reflect 读取未导出字段/嵌入 struct/接口字段，跨 GC 保持并可安全调用。
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

type node struct {
	V     int64
	Label string
}

type namedNode struct {
	*node
}

func (n *namedNode) String() string { return n.Label }

type meta struct {
	hidden int64
	secret string
	ratios [2]float64
}

type inner struct {
	Tag  *node
	priv int64
	Note string
}

type outer struct {
	meta
	Inner   inner
	Iface   fmt.Stringer
	Any     any
	hidden2 int64 `u:"hidden2"`
}

var (
	failed  int32
	gcSink  [][]byte
	anySink any
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
		fmt.Printf("FAILED %s seed=%d (%d failed checks)\n", name, uafSeed, n)
		os.Exit(1)
	}
	fmt.Println("ALL PASS " + name)
}

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func main() {
	uafSeed = envSeed()
	const rounds = 20

	for r := 0; r < rounds; r++ {
		rnd := rand.New(rand.NewSource(uafSeed + int64(r)*97))

		hidden := int64(3 + rnd.Intn(9))
		secret := fmt.Sprintf("s%02d", rnd.Intn(20))
		ratios := [2]float64{float64(rnd.Intn(10)) / 4, float64(rnd.Intn(10)) / 8}
		tagV := 100 + int64(rnd.Intn(50))
		note := fmt.Sprintf("note-%d", rnd.Intn(5))

		labels := []string{"alpha", "beta", "gamma"}
		order := rnd.Perm(len(labels))
		attr := make(map[string]*node, len(labels))
		var wantSum int64
		for _, i := range order {
			p := &node{V: int64(i+1)*11 + tagV%7, Label: labels[i]}
			attr[labels[i]] = p
			wantSum += p.V
		}

		o := &outer{
			meta:    meta{hidden: hidden, secret: secret, ratios: ratios},
			Inner:   inner{Tag: &node{V: tagV, Label: "inner"}, priv: hidden * 2, Note: note},
			Iface:   &namedNode{&node{V: tagV, Label: "iface"}},
			Any:     attr,
			hidden2: hidden + 1,
		}
		anySink = o
		gcStress()

		typ := reflect.TypeOf(outer{})
		ck(typ.NumField() == 5, "outer field count")
		f0 := typ.Field(0)
		ck(f0.Name == "meta" && f0.Anonymous && f0.PkgPath != "", "embedded unexported struct metadata")
		hf, ok := typ.FieldByName("hidden2")
		ck(ok && hf.PkgPath != "" && hf.Tag.Get("u") == "hidden2", "unexported field tag readable")

		v := reflect.ValueOf(o).Elem()
		mv := v.Field(0)
		ck(!mv.CanInterface() && !mv.CanSet(), "unexported embedded struct is read-only")
		ck(mv.FieldByName("hidden").Int() == hidden, "read unexported int via reflect")
		ck(mv.FieldByName("secret").String() == secret, "read unexported string via reflect")
		ck(mv.FieldByName("ratios").Index(1).Float() == ratios[1], "read unexported array element")
		ck(mv.FieldByName("hidden").CanInterface() == false, "unexported field cannot Interface")

		ck(v.FieldByName("Inner").CanInterface() && v.FieldByName("Inner").CanSet(), "exported nested struct is writable")
		priv := v.FieldByName("Inner").FieldByName("priv")
		ck(!priv.CanInterface() && priv.Int() == hidden*2, "read unexported nested field")
		ck(v.FieldByName("Inner").FieldByName("Note").String() == note, "read exported nested field")
		ck(v.FieldByName("Inner").FieldByName("Tag").Elem().FieldByName("V").Int() == tagV, "nested pointer field after GC")
		ck(v.FieldByName("hidden").Int() == hidden, "promoted unexported field via FieldByName")
		ck(!v.FieldByName("hidden").CanInterface(), "promoted unexported field is read-only")

		iv := v.FieldByName("Iface")
		ck(iv.Kind() == reflect.Interface && iv.Elem().Kind() == reflect.Ptr, "interface field shape")
		ck(iv.Interface().(fmt.Stringer).String() == "iface", "interface field dynamic value")
		ck(iv.MethodByName("String").Call(nil)[0].String() == "iface", "interface method via reflect")
		ck(iv.Elem().MethodByName("String").Call(nil)[0].String() == "iface", "concrete method via reflect")

		av := v.FieldByName("Any")
		ck(av.Kind() == reflect.Interface && !av.IsNil() && av.Elem().Kind() == reflect.Map, "any field holds map")
		ck(av.Elem().Len() == len(attr), "any map length")
		var got int64
		for _, k := range av.Elem().MapKeys() {
			got += av.Elem().MapIndex(k).Elem().FieldByName("V").Int()
		}
		ck(got == wantSum, "any map pointer values after GC")
		ck(av.Elem().MapKeys()[0].CanInterface(), "map key of any field is readable")

		// 未导出字段的 Interface/Set 必须 panic。
		func() {
			defer func() { ck(recover() != nil, "Interface on unexported field must panic") }()
			_ = priv.Interface()
		}()
		func() {
			defer func() { ck(recover() != nil, "Set on unexported field must panic") }()
			priv.SetInt(1)
		}()

		// 写导出字段，GC 后从全局槽复读。
		v.FieldByName("Inner").FieldByName("Note").SetString("updated")
		ck(o.Inner.Note == "updated", "write exported nested field")
		o = nil
		gcStress()

		g := anySink.(*outer)
		ck(g.Inner.Note == "updated", "global interface slot keeps outer alive")
		ck(g.Inner.Tag.V == tagV && g.Iface.String() == "iface", "nested pointers survive repeated GC")
		gv := reflect.ValueOf(g).Elem()
		ck(gv.Field(0).FieldByName("hidden").Int() == hidden, "unexported value stable after GC")
		ck(gv.FieldByName("Any").Elem().Len() == len(attr), "interface map stable after GC")

		// 类型信息缓存不受 GC 影响。
		ck(reflect.TypeOf(g.Inner.Tag) == reflect.TypeOf((*node)(nil)), "type identity stable after GC")
		innerSF, innerOK := typ.FieldByName("Inner")
		ck(innerOK && innerSF.Type.Kind() == reflect.Struct, "field type metadata stable")
	}
	finish("l06_reflect_unexported_iface")
}
