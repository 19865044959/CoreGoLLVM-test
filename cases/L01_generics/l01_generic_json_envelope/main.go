// L01: 泛型序列化边界——Envelope[T] 的 JSON 往返、nil/空/零值区分、
// 自定义 Marshaler 的泛型 Pair[T]、严格解码错误与 RawMessage 边界。
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Envelope[T any] struct {
	Kind  string          `json:"kind"`
	Seq   int             `json:"seq"`
	Data  *T              `json:"data"`
	Extra map[string]int  `json:"extra,omitempty"`
	Raw   json.RawMessage `json:"raw,omitempty"`
}

type Pair[T any] struct {
	A T
	B *T
}

func (p Pair[T]) MarshalJSON() ([]byte, error) {
	type wire struct {
		A T  `json:"a"`
		B *T `json:"b"`
	}
	return json.Marshal(wire{A: p.A, B: p.B})
}

var _ json.Marshaler = Pair[item]{}

func decodeStrict[T any](b []byte) (Envelope[T], error) {
	var e Envelope[T]
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	err := dec.Decode(&e)
	return e, err
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

	// 结构体字段顺序 + map 键排序 => 确定 golden。
	p3 := item{ID: 3, Name: "x"}
	e := Envelope[item]{
		Kind:  "item",
		Seq:   7,
		Data:  &p3,
		Extra: map[string]int{"b": 2, "a": 1},
		Raw:   json.RawMessage(`{"z":1}`),
	}
	b, err := json.Marshal(e)
	ck(err == nil, "marshal envelope failed")
	const want = `{"kind":"item","seq":7,"data":{"id":3,"name":"x"},"extra":{"a":1,"b":2},"raw":{"z":1}}`
	ck(string(b) == want, fmt.Sprintf("envelope golden mismatch got=%s", string(b)))
	ck(json.Valid(b), "envelope output is not valid JSON")

	var rt Envelope[item]
	ck(json.Unmarshal(b, &rt) == nil, "unmarshal envelope failed")
	ck(rt.Kind == "item" && rt.Seq == 7, "envelope scalar fields lost in roundtrip")
	ck(rt.Data != nil && rt.Data.ID == 3 && rt.Data.Name == "x", "envelope payload lost in roundtrip")
	ck(len(rt.Extra) == 2 && rt.Extra["a"] == 1 && rt.Extra["b"] == 2, "extra map lost in roundtrip")
	ck(string(rt.Raw) == `{"z":1}`, "raw message not preserved")

	// 零值 / nil / 空 的边界。
	bz, err := json.Marshal(Envelope[int64]{})
	ck(err == nil && string(bz) == `{"kind":"","seq":0,"data":null}`, "zero-value envelope golden mismatch")

	emptySlice := []string{}
	be, err := json.Marshal(Envelope[[]string]{Kind: "s", Data: &emptySlice})
	ck(err == nil && string(be) == `{"kind":"s","seq":0,"data":[]}`, "empty slice envelope golden mismatch")
	bn, err := json.Marshal(Envelope[[]string]{Kind: "s"})
	ck(err == nil && string(bn) == `{"kind":"s","seq":0,"data":null}`, "nil slice envelope golden mismatch")

	var rs Envelope[[]string]
	ck(json.Unmarshal(be, &rs) == nil && rs.Data != nil && len(*rs.Data) == 0,
		"empty slice did not roundtrip as non-nil empty")
	var rn Envelope[[]string]
	ck(json.Unmarshal(bn, &rn) == nil && rn.Data == nil, "null data did not roundtrip as nil")

	emptyMap := map[string]int{}
	bem, err := json.Marshal(Envelope[map[string]int]{Data: &emptyMap})
	ck(err == nil && string(bem) == `{"kind":"","seq":0,"data":{}}`, "empty map envelope golden mismatch")
	var rem Envelope[map[string]int]
	ck(json.Unmarshal(bem, &rem) == nil && rem.Data != nil && len(*rem.Data) == 0,
		"empty map did not roundtrip as non-nil empty")

	fullMap := map[string]int{"z": 26, "a": 1}
	bm, err := json.Marshal(Envelope[map[string]int]{Kind: "m", Seq: 2, Data: &fullMap})
	ck(err == nil, "marshal map envelope failed")
	var fm Envelope[map[string]int]
	ck(json.Unmarshal(bm, &fm) == nil && fm.Data != nil && len(*fm.Data) == 2 &&
		(*fm.Data)["a"] == 1 && (*fm.Data)["z"] == 26, "map payload lost in roundtrip")

	// 泛型类型的自定义 Marshaler。
	qa := item{ID: 1, Name: "a"}
	qb := item{ID: 2, Name: "b"}
	pb, err := json.Marshal(Pair[item]{A: qa, B: &qb})
	ck(err == nil, "marshal generic pair failed")
	ck(string(pb) == `{"a":{"id":1,"name":"a"},"b":{"id":2,"name":"b"}}`, "generic pair golden mismatch")
	var pr Pair[item]
	ck(json.Unmarshal(pb, &pr) == nil, "unmarshal generic pair failed")
	ck(pr.A.ID == 1 && pr.A.Name == "a" && pr.B != nil && pr.B.ID == 2 && pr.B.Name == "b",
		"generic pair roundtrip mismatch")

	// 严格解码：未知字段/坏类型/坏 JSON 都必须报错，不得 panic。
	valid := []byte(`{"kind":"k","seq":1,"data":{"id":1,"name":"x"}}`)
	se, err := decodeStrict[item](valid)
	ck(err == nil && se.Data != nil && se.Data.ID == 1, "strict decode rejected valid envelope")
	_, err = decodeStrict[item]([]byte(`{"kind":"k","seq":1,"data":{"id":1,"name":"x"},"bogus":1}`))
	ck(err != nil, "strict decode accepted unknown top-level field")
	_, err = decodeStrict[item]([]byte(`{"kind":"k","seq":1,"data":{"id":"bad"}}`))
	ck(err != nil, "strict decode accepted wrong payload type")
	_, err = decodeStrict[item]([]byte(`{"kind":`))
	ck(err != nil, "malformed JSON must return error")

	var nullEnv Envelope[item]
	ck(json.Unmarshal([]byte("null"), &nullEnv) == nil, "top-level null must decode without error")
	ck(nullEnv.Data == nil && nullEnv.Kind == "" && nullEnv.Seq == 0, "top-level null produced non-zero envelope")

	// 非法 RawMessage 在 Marshal 时报错。
	_, err = json.Marshal(Envelope[int64]{Raw: json.RawMessage(`not-json`)})
	ck(err != nil, "invalid raw message must fail marshal")

	// 批量往返 + GC：解码后的数据在 GC 后仍完整。
	count := 14 + int(uafSeed%9)
	kept := make([]*item, 0, count)
	for i := 0; i < count; i++ {
		it := item{ID: int64(i), Name: fmt.Sprintf("i%d", i)}
		env := Envelope[item]{Kind: "batch", Seq: i, Data: &it, Extra: map[string]int{"i": i}}
		bb, err := json.Marshal(env)
		ck(err == nil, "batch marshal failed")
		var out Envelope[item]
		ck(json.Unmarshal(bb, &out) == nil, "batch unmarshal failed")
		ck(out.Data != nil && out.Data.ID == int64(i) && out.Data.Name == fmt.Sprintf("i%d", i),
			"batch payload mismatch")
		ck(out.Seq == i && out.Extra["i"] == i, "batch metadata mismatch")
		if out.Data != nil {
			kept = append(kept, out.Data)
		}
		if i%4 == 0 {
			gcStress()
		}
	}
	gcStress()
	ck(len(kept) == count, "batch lost decoded payloads")
	for i, p := range kept {
		ck(p != nil && p.ID == int64(i), "decoded payload corrupted after GC")
		if p != nil {
			ck(p.Name == fmt.Sprintf("i%d", i), "decoded payload name corrupted after GC")
		}
	}

	finish("l01_generic_json_envelope")
}
