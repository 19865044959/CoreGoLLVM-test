// L10: RPC codec 注册表：名称/别名归一、最新注册覆盖、内容协商（q 值）、
// 快照隔离、冻结、codec+gzip 管线、GC 与随机注册/协商对账。
package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// ---- codec ----

type codec interface {
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

type jsonCodec struct{}

func (jsonCodec) Name() string                       { return "json" }
func (jsonCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

type textCodec struct{}

func (textCodec) Name() string { return "text" }
func (textCodec) Marshal(v any) ([]byte, error) {
	switch x := v.(type) {
	case string:
		return []byte(x), nil
	case []byte:
		return append([]byte(nil), x...), nil
	}
	return nil, errors.New("text codec: unsupported value")
}
func (textCodec) Unmarshal(data []byte, v any) error {
	switch x := v.(type) {
	case *string:
		*x = string(data)
		return nil
	case *[]byte:
		*x = append([]byte(nil), data...)
		return nil
	}
	return errors.New("text codec: unsupported target")
}

type binaryCodec struct{}

func (binaryCodec) Name() string { return "binary" }
func (binaryCodec) Marshal(v any) ([]byte, error) {
	switch x := v.(type) {
	case []byte:
		return append([]byte(nil), x...), nil
	case string:
		return []byte(x), nil
	}
	return nil, errors.New("binary codec: unsupported value")
}
func (binaryCodec) Unmarshal(data []byte, v any) error {
	switch x := v.(type) {
	case *[]byte:
		*x = append([]byte(nil), data...)
		return nil
	case *string:
		*x = string(data)
		return nil
	}
	return errors.New("binary codec: unsupported target")
}

// ---- codec 注册表 ----

var (
	errFrozen     = errors.New("codec registry frozen")
	errNilCodec   = errors.New("nil codec")
	errBadName    = errors.New("empty codec name")
	errNoCodec    = errors.New("no codec registered for name")
	errNoAccept   = errors.New("no acceptable codec")
	errDupAlias   = errors.New("duplicate codec alias")
	errCompFrozen = errors.New("compressor registry frozen")
)

func resolveName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	switch s {
	case "application/json", "application/x-json":
		return "json"
	case "text/plain":
		return "text"
	case "application/octet-stream":
		return "binary"
	}
	return s
}

type codecRegistry struct {
	mu      sync.RWMutex
	m       map[string]codec
	aliases map[string]string
	def     string
	frozen  bool
}

func newCodecRegistry() *codecRegistry {
	return &codecRegistry{m: make(map[string]codec), aliases: make(map[string]string)}
}

func (r *codecRegistry) Register(name string, c codec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return errFrozen
	}
	if c == nil {
		return errNilCodec
	}
	key := resolveName(name)
	if key == "" {
		return errBadName
	}
	r.m[key] = c // 最新注册覆盖
	return nil
}

func (r *codecRegistry) RegisterAlias(alias, target string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return errFrozen
	}
	a, t := resolveName(alias), resolveName(target)
	if a == "" || t == "" {
		return errBadName
	}
	if _, ok := r.m[t]; !ok {
		return fmt.Errorf("alias target %q not registered", t)
	}
	if _, ok := r.aliases[a]; ok {
		return errDupAlias
	}
	r.aliases[a] = t
	return nil
}

func (r *codecRegistry) lookupLocked(name string) (codec, string, bool) {
	key := resolveName(name)
	if c, ok := r.m[key]; ok {
		return c, key, true
	}
	if target, ok := r.aliases[key]; ok {
		c, ok := r.m[target]
		return c, target, ok
	}
	return nil, "", false
}

func (r *codecRegistry) Get(name string) (codec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, _, ok := r.lookupLocked(name)
	return c, ok
}

func (r *codecRegistry) SetDefault(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, key, ok := r.lookupLocked(name)
	if !ok {
		return errNoCodec
	}
	r.def = key
	return nil
}

func (r *codecRegistry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func (r *codecRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.namesLocked()
}

func (r *codecRegistry) namesLocked() []string {
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type codecSnapshot struct {
	m       map[string]codec
	aliases map[string]string
	def     string
	names   []string
}

func (s *codecSnapshot) lookup(name string) (codec, string, bool) {
	key := resolveName(name)
	if c, ok := s.m[key]; ok {
		return c, key, true
	}
	if target, ok := s.aliases[key]; ok {
		c, ok := s.m[target]
		return c, target, ok
	}
	return nil, "", false
}

func (s *codecSnapshot) Get(name string) (codec, bool) {
	c, _, ok := s.lookup(name)
	return c, ok
}

func (s *codecSnapshot) Names() []string { return append([]string(nil), s.names...) }
func (s *codecSnapshot) Default() string { return s.def }

func (r *codecRegistry) Snapshot() *codecSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := &codecSnapshot{
		m:       make(map[string]codec, len(r.m)),
		aliases: make(map[string]string, len(r.aliases)),
		def:     r.def,
		names:   r.namesLocked(),
	}
	for k, v := range r.m {
		s.m[k] = v
	}
	for k, v := range r.aliases {
		s.aliases[k] = v
	}
	return s
}

func (r *codecRegistry) Negotiate(contentType, accept string) (codec, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if strings.TrimSpace(contentType) != "" {
		c, _, ok := r.lookupLocked(contentType)
		if !ok {
			return nil, fmt.Errorf("content-type %q: %w", contentType, errNoCodec)
		}
		return c, nil
	}
	type cand struct {
		c     codec
		q     float64
		order int
	}
	var cands []cand
	for i, part := range strings.Split(accept, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, ";")
		name := strings.TrimSpace(fields[0])
		q := 1.0
		for _, p := range fields[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(strings.ToLower(p), "q=") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(p[2:]), 64); err == nil {
					q = v
				}
			}
		}
		if q <= 0 {
			continue
		}
		if c, _, ok := r.lookupLocked(name); ok {
			cands = append(cands, cand{c: c, q: q, order: i})
		}
	}
	if len(cands) > 0 {
		sort.SliceStable(cands, func(i, j int) bool {
			if cands[i].q != cands[j].q {
				return cands[i].q > cands[j].q
			}
			return cands[i].order < cands[j].order
		})
		return cands[0].c, nil
	}
	if strings.TrimSpace(accept) != "" {
		return nil, errNoAccept
	}
	if r.def == "" {
		return nil, errNoCodec
	}
	if c, ok := r.m[r.def]; ok {
		return c, nil
	}
	return nil, errNoCodec
}

// ---- compressor ----

type compressor interface {
	Name() string
	Compress([]byte) ([]byte, error)
	Decompress([]byte) ([]byte, error)
}

type gzipCompressor struct{}

func (gzipCompressor) Name() string { return "gzip" }
func (gzipCompressor) Compress(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
func (gzipCompressor) Decompress(b []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(r)
	cerr := r.Close()
	if err != nil {
		return nil, err
	}
	if cerr != nil {
		return nil, cerr
	}
	return out, nil
}

type compressorRegistry struct {
	mu     sync.RWMutex
	m      map[string]compressor
	frozen bool
}

func newCompressorRegistry() *compressorRegistry {
	return &compressorRegistry{m: make(map[string]compressor)}
}

func resolveCompressorName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	if i := strings.IndexByte(s, ';'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	if s == "x-gzip" {
		return "gzip"
	}
	return s
}

func (r *compressorRegistry) Register(name string, c compressor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return errCompFrozen
	}
	if c == nil {
		return errNilCodec
	}
	key := resolveCompressorName(name)
	if key == "" {
		return errBadName
	}
	r.m[key] = c
	return nil
}

func (r *compressorRegistry) Get(name string) (compressor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.m[resolveCompressorName(name)]
	return c, ok
}

func (r *compressorRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.m))
	for k := range r.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (r *compressorRegistry) Freeze() {
	r.mu.Lock()
	r.frozen = true
	r.mu.Unlock()
}

func encodeThrough(cr *codecRegistry, comp *compressorRegistry, codecName, encoding string, v any) ([]byte, error) {
	c, ok := cr.Get(codecName)
	if !ok {
		return nil, fmt.Errorf("codec %q: %w", codecName, errNoCodec)
	}
	data, err := c.Marshal(v)
	if err != nil {
		return nil, err
	}
	enc := resolveCompressorName(encoding)
	if enc == "" || enc == "identity" {
		return data, nil
	}
	z, ok := comp.Get(enc)
	if !ok {
		return nil, fmt.Errorf("encoding %q: %w", encoding, errNoCodec)
	}
	return z.Compress(data)
}

func decodeThrough(cr *codecRegistry, comp *compressorRegistry, codecName, encoding string, data []byte, v any) error {
	c, ok := cr.Get(codecName)
	if !ok {
		return fmt.Errorf("codec %q: %w", codecName, errNoCodec)
	}
	enc := resolveCompressorName(encoding)
	if enc != "" && enc != "identity" {
		z, ok := comp.Get(enc)
		if !ok {
			return fmt.Errorf("encoding %q: %w", encoding, errNoCodec)
		}
		var err error
		data, err = z.Decompress(data)
		if err != nil {
			return err
		}
	}
	return c.Unmarshal(data, v)
}

// ---- 测试 ----

type payload struct {
	A int32  `json:"a"`
	B string `json:"b"`
}

func main() {
	seed := int64(1)
	if v := os.Getenv("UAF_SEED"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			seed = n
		}
	}
	curSeed = seed
	rounds := 1
	if v := os.Getenv("TORTURE_ROUNDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			rounds = n
			if rounds > 100 {
				rounds = 100
			}
		}
	}

	reg := newCodecRegistry()
	ck(reg.Register("json", jsonCodec{}) == nil, "register json")
	ck(reg.Register("text/plain", textCodec{}) == nil, "register text alias")
	ck(reg.Register("application/octet-stream", binaryCodec{}) == nil, "register binary alias")
	ck(reg.Register("", jsonCodec{}) == errBadName, "empty name rejected")
	ck(reg.Register("nil", nil) == errNilCodec, "nil codec rejected")
	ck(reg.RegisterAlias("plain", "text") == nil, "register alias")
	ck(reg.RegisterAlias("plain", "text") == errDupAlias, "duplicate alias rejected")
	ck(reg.RegisterAlias("bad", "missing") != nil, "alias target must exist")

	names := reg.Names()
	ck(len(names) == 3 && names[0] == "binary" && names[1] == "json" && names[2] == "text", "sorted names after alias normalization")
	if c, ok := reg.Get("application/json; charset=utf-8"); !ok || c.Name() != "json" {
		ck(false, "mime content type resolves json")
	}
	if c, ok := reg.Get("PLAIN"); !ok || c.Name() != "text" {
		ck(false, "alias is case-insensitive")
	}
	if _, ok := reg.Get("unknown"); ok {
		ck(false, "unknown codec absent")
	}
	// 最新注册覆盖。
	ck(reg.Register("json", binaryCodec{}) == nil, "override json")
	if c, ok := reg.Get("json"); !ok || c.Name() != "binary" {
		ck(false, "latest registration wins")
	}
	ck(reg.Register("json", jsonCodec{}) == nil, "restore json")

	// 默认与协商。
	ck(reg.SetDefault("json") == nil, "set default")
	ck(reg.SetDefault("missing") == errNoCodec, "set unknown default")
	c, err := reg.Negotiate("", "")
	ck(err == nil && c.Name() == "json", "negotiate default")
	c, err = reg.Negotiate("text/plain; charset=x", "json")
	ck(err == nil && c.Name() == "text", "content-type takes precedence")
	_, err = reg.Negotiate("unknown/type", "json")
	ck(errors.Is(err, errNoCodec), "unknown content type error")
	c, err = reg.Negotiate("", "application/x-json, text/plain")
	ck(err == nil && c.Name() == "json", "accept first supported")
	c, err = reg.Negotiate("", "nope, text/plain;q=0.9, binary;q=0.1")
	ck(err == nil && c.Name() == "text", "accept q values")
	c, err = reg.Negotiate("", "json;q=0.1, text;q=0.9")
	ck(err == nil && c.Name() == "text", "higher q wins")
	c, err = reg.Negotiate("", "json;q=0.9, text;q=0.9")
	ck(err == nil && c.Name() == "json", "q tie keeps order")
	c, err = reg.Negotiate("", "json;q=0, text;q=0.5")
	ck(err == nil && c.Name() == "text", "q=0 not acceptable")
	_, err = reg.Negotiate("", "nope;q=1")
	ck(errors.Is(err, errNoAccept), "no acceptable codec")
	ck(reg.SetDefault("text") == nil, "switch default")
	c, err = reg.Negotiate("", "")
	ck(err == nil && c.Name() == "text", "default updated")
	ck(reg.SetDefault("json") == nil, "restore default")

	// 快照隔离与冻结。
	snap := reg.Snapshot()
	ck(len(snap.Names()) == 3 && snap.Default() == "json", "snapshot content")
	ck(reg.Register("late", textCodec{}) == nil, "register late")
	ck(len(snap.Names()) == 3, "snapshot unchanged by later register")
	if _, ok := snap.Get("late"); ok {
		ck(false, "snapshot must not see late codec")
	}
	if c, ok := snap.Get("plain"); !ok || c.Name() != "text" {
		ck(false, "snapshot keeps aliases")
	}
	reg.Freeze()
	ck(reg.Register("frozen", textCodec{}) == errFrozen, "frozen registry rejects register")
	if c, ok := reg.Get("json"); !ok || c.Name() != "json" {
		ck(false, "frozen registry still serves")
	}

	// compressor 注册表与管线。
	comp := newCompressorRegistry()
	ck(comp.Register("gzip", gzipCompressor{}) == nil, "register gzip")
	ck(comp.Register("", gzipCompressor{}) == errBadName, "empty compressor name")
	ck(comp.Register("nil", nil) == errNilCodec, "nil compressor")
	cnames := comp.Names()
	ck(len(cnames) == 1 && cnames[0] == "gzip", "compressor names")
	if z, ok := comp.Get("X-GZIP; level=1"); !ok || z.Name() != "gzip" {
		ck(false, "compressor alias")
	}
	want := payload{A: 42, B: "hello"}
	for _, enc := range []string{"", "identity", "gzip", "X-GZIP; level=1"} {
		data, err := encodeThrough(reg, comp, "application/json", enc, &want)
		ck(err == nil && len(data) > 0, "pipeline encode "+enc)
		var got payload
		if err := decodeThrough(reg, comp, "json", enc, data, &got); err != nil || got != want {
			ck(false, "pipeline roundtrip "+enc)
		}
	}
	if _, err := encodeThrough(reg, comp, "missing", "", &want); err == nil {
		ck(false, "unknown codec encode error")
	}
	if _, err := encodeThrough(reg, comp, "json", "br", &want); err == nil {
		ck(false, "unknown encoding encode error")
	}
	var sink payload
	if err := decodeThrough(reg, comp, "json", "gzip", []byte("not gzip"), &sink); err == nil {
		ck(false, "corrupt compressed data rejected")
	}
	txt, err := encodeThrough(reg, comp, "text", "gzip", "plain text")
	if err != nil {
		ck(false, "text encode")
	}
	var gotText string
	if err := decodeThrough(reg, comp, "text", "gzip", txt, &gotText); err != nil || gotText != "plain text" {
		ck(false, "text roundtrip")
	}
	raw, err := encodeThrough(reg, comp, "binary", "", []byte{1, 2, 3})
	if err != nil {
		ck(false, "binary encode")
	}
	var gotRaw []byte
	if err := decodeThrough(reg, comp, "binary", "", raw, &gotRaw); err != nil || !bytes.Equal(gotRaw, []byte{1, 2, 3}) {
		ck(false, "binary roundtrip")
	}
	comp.Freeze()
	ck(comp.Register("zstd", gzipCompressor{}) == errCompFrozen, "frozen compressor registry")

	// GC：保留快照/注册表与压缩结果，丢弃临时对象后仍可用。
	keptSnap := snap
	keptData := txt
	gcStress()
	if c, ok := keptSnap.Get("text"); !ok || c.Name() != "text" {
		ck(false, "snapshot survives GC")
	}
	var gotText2 string
	if err := decodeThrough(reg, comp, "text", "gzip", keptData, &gotText2); err != nil || gotText2 != "plain text" {
		ck(false, "data survives GC")
	}
	keptSnap = nil
	gcStress()

	// 随机注册/查询/协商/编解码。
	r := rand.New(rand.NewSource(seed))
	rreg := newCodecRegistry()
	base := []struct {
		name string
		c    codec
	}{
		{"json", jsonCodec{}},
		{"text/plain", textCodec{}},
		{"application/octet-stream", binaryCodec{}},
	}
	for _, b := range base {
		if err := rreg.Register(b.name, b.c); err != nil {
			ckSeed(false, "base register")
		}
	}
	rreg.SetDefault("json")
	rcomp := newCompressorRegistry()
	rcomp.Register("gzip", gzipCompressor{})
	model := make(map[string]string)
	for _, b := range base {
		model[resolveName(b.name)] = b.c.Name()
	}
	registerNames := []string{"text", "binary", "s0", "s1", "s2"}
	queryNames := []string{"json", "text", "binary", "application/x-json", "TEXT/PLAIN", "application/octet-stream", "s0", "s1", "s2"}
	codecChoices := []codec{jsonCodec{}, textCodec{}, binaryCodec{}}
	for i := 0; i < rounds*80; i++ {
		switch r.Intn(4) {
		case 0:
			name := registerNames[r.Intn(len(registerNames))]
			c := codecChoices[r.Intn(len(codecChoices))]
			if err := rreg.Register(name, c); err != nil {
				ckSeed(false, "random register")
			}
			model[resolveName(name)] = c.Name()
		case 1:
			name := queryNames[r.Intn(len(queryNames))]
			c, ok := rreg.Get(name)
			wantName, wantOK := model[resolveName(name)]
			if ok != wantOK || (ok && c.Name() != wantName) {
				ckSeed(false, "random get")
			}
		case 2:
			// 协商：用注册表中的键构造 accept 列表。
			keys := make([]string, 0, len(model))
			for k := range model {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			accept := keys[r.Intn(len(keys))]
			if r.Intn(2) == 0 {
				accept += ";q=0.5, nope;q=1"
			}
			c, err := rreg.Negotiate("", accept)
			if err != nil || c == nil || c.Name() != model[resolveName(accept)] {
				ckSeed(false, "random negotiate")
			}
		case 3:
			cname := []string{"json", "application/x-json"}[r.Intn(2)]
			enc := []string{"", "identity", "gzip"}[r.Intn(3)]
			p := payload{A: int32(r.Intn(1000)), B: "r" + strconv.Itoa(r.Intn(100))}
			data, err := encodeThrough(rreg, rcomp, cname, enc, &p)
			if err != nil {
				ckSeed(false, "random encode")
				continue
			}
			var out payload
			if err := decodeThrough(rreg, rcomp, cname, enc, data, &out); err != nil || out != p {
				ckSeed(false, "random roundtrip")
			}
		}
		if i%40 == 39 {
			gcStress()
			rs := rreg.Snapshot()
			sn := rs.Names()
			ckSeed(sort.StringsAreSorted(sn), "random snapshot names sorted")
			ckSeed(len(sn) == len(model), "random snapshot size")
			for _, k := range sn {
				c, ok := rs.Get(k)
				ckSeed(ok && c.Name() == model[k], "random snapshot entry")
			}
		}
	}

	finish("l10_codec_registry")
}
