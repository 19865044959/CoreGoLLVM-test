// L10: 生成风格 RPC stub：请求/响应结构体、codec 注册表、长度前缀编解码、错误路径与 GC。
// P2：TORTURE_ROUNDS 放大调用轮次。
package main

import (
	"bytes"
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
	ErrUnimplemented     = errors.New("rpc: unimplemented")
	ErrResourceExhausted = errors.New("rpc: resource exhausted")
	ErrInternal          = errors.New("rpc: internal")
	ErrInvalidResponse   = errors.New("rpc: invalid response")
	ErrNilRequest        = errors.New("rpc: nil request")
	ErrUnknownCodec      = errors.New("rpc: unknown codec")
	errTransport         = errors.New("transport: connection reset")
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

func envInt(name string, def int) int {
	s := os.Getenv(name)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		return def
	}
	return v
}

// ---- protobuf 风格 wire 辅助 ----

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func readVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b); i++ {
		if i == 9 && b[i] > 1 {
			return 0, 0, errors.New("varint overflow")
		}
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("truncated varint")
}

func appendBytes(b []byte, field int, p []byte) []byte {
	b = appendVarint(b, uint64(field<<3|2))
	b = appendVarint(b, uint64(len(p)))
	return append(b, p...)
}

func readBytes(b []byte) ([]byte, int, error) {
	ln, n, err := readVarint(b)
	if err != nil {
		return nil, 0, err
	}
	if ln > uint64(len(b)-n) {
		return nil, 0, errors.New("truncated bytes")
	}
	return b[n : n+int(ln)], n + int(ln), nil
}

// ---- 生成风格请求/响应 ----

type request struct {
	Method   string
	Seq      uint64
	Body     []byte
	Deadline int64
}

type response struct {
	Seq  uint64
	Code int32
	Body []byte
	Msg  string
}

type codec interface {
	Name() string
	MarshalRequest(*request) ([]byte, error)
	UnmarshalRequest([]byte) (*request, error)
	MarshalResponse(*response) ([]byte, error)
	UnmarshalResponse([]byte) (*response, error)
}

type binaryCodec struct{}

func (binaryCodec) Name() string { return "binary" }

func (binaryCodec) MarshalRequest(r *request) ([]byte, error) {
	if r == nil {
		return nil, ErrNilRequest
	}
	var b []byte
	if r.Method != "" {
		b = appendBytes(b, 1, []byte(r.Method))
	}
	if r.Seq != 0 {
		b = appendVarint(b, uint64(2<<3))
		b = appendVarint(b, r.Seq)
	}
	if r.Body != nil {
		b = appendBytes(b, 3, r.Body)
	}
	if r.Deadline != 0 {
		b = appendVarint(b, uint64(4<<3))
		b = appendVarint(b, uint64(r.Deadline))
	}
	return b, nil
}

func (binaryCodec) UnmarshalRequest(b []byte) (*request, error) {
	r := &request{}
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		switch int(key >> 3) {
		case 1:
			v, n, err := readBytes(b)
			if err != nil {
				return nil, err
			}
			r.Method = string(v)
			b = b[n:]
		case 2:
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			r.Seq = v
			b = b[n:]
		case 3:
			v, n, err := readBytes(b)
			if err != nil {
				return nil, err
			}
			cp := make([]byte, len(v))
			copy(cp, v)
			r.Body = cp
			b = b[n:]
		case 4:
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			r.Deadline = int64(v)
			b = b[n:]
		default:
			return nil, errors.New("unknown request field")
		}
	}
	return r, nil
}

func (binaryCodec) MarshalResponse(r *response) ([]byte, error) {
	if r == nil {
		return nil, errors.New("nil response")
	}
	var b []byte
	b = appendVarint(b, uint64(1<<3))
	b = appendVarint(b, r.Seq)
	if r.Code != 0 {
		b = appendVarint(b, uint64(2<<3))
		b = appendVarint(b, uint64(r.Code))
	}
	if r.Body != nil {
		b = appendBytes(b, 3, r.Body)
	}
	if r.Msg != "" {
		b = appendBytes(b, 4, []byte(r.Msg))
	}
	return b, nil
}

func (binaryCodec) UnmarshalResponse(b []byte) (*response, error) {
	r := &response{}
	seenSeq := false
	for len(b) > 0 {
		key, n, err := readVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		switch int(key >> 3) {
		case 1:
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			r.Seq = v
			seenSeq = true
			b = b[n:]
		case 2:
			v, n, err := readVarint(b)
			if err != nil {
				return nil, err
			}
			r.Code = int32(v)
			b = b[n:]
		case 3:
			v, n, err := readBytes(b)
			if err != nil {
				return nil, err
			}
			cp := make([]byte, len(v))
			copy(cp, v)
			r.Body = cp
			b = b[n:]
		case 4:
			v, n, err := readBytes(b)
			if err != nil {
				return nil, err
			}
			r.Msg = string(v)
			b = b[n:]
		default:
			return nil, errors.New("unknown response field")
		}
	}
	if !seenSeq {
		return nil, errors.New("missing seq")
	}
	return r, nil
}

// faultyCodec 用于错误注入（模拟坏 codec）。
type faultyCodec struct {
	base          codec
	name          string
	failMarshal   bool
	failUnmarshal bool
}

func (f *faultyCodec) Name() string {
	if f.name != "" {
		return f.name
	}
	return "faulty"
}
func (f *faultyCodec) MarshalRequest(r *request) ([]byte, error) {
	if f.failMarshal {
		return nil, errors.New("codec: marshal exploded")
	}
	return f.base.MarshalRequest(r)
}
func (f *faultyCodec) UnmarshalRequest(b []byte) (*request, error) {
	return f.base.UnmarshalRequest(b)
}
func (f *faultyCodec) MarshalResponse(r *response) ([]byte, error) {
	return f.base.MarshalResponse(r)
}
func (f *faultyCodec) UnmarshalResponse(b []byte) (*response, error) {
	if f.failUnmarshal {
		return nil, errors.New("codec: unmarshal exploded")
	}
	return f.base.UnmarshalResponse(b)
}

// ---- codec 注册表（模拟 grpc encoding 注册表） ----

type codecRegistry struct {
	byName map[string]codec
	order  []string
}

func newCodecRegistry() *codecRegistry {
	return &codecRegistry{byName: make(map[string]codec)}
}

func (r *codecRegistry) register(c codec) error {
	if c == nil || c.Name() == "" {
		return ErrUnknownCodec
	}
	if _, ok := r.byName[c.Name()]; !ok {
		r.order = append(r.order, c.Name())
	}
	r.byName[c.Name()] = c // 与 grpc-go 一致：同名后注册者覆盖
	return nil
}

func (r *codecRegistry) get(name string) (codec, error) {
	c, ok := r.byName[name]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCodec, name)
	}
	return c, nil
}

func (r *codecRegistry) names() []string {
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}

// ---- 传输层与 stub ----

type transport interface {
	RoundTrip(service, method string, payload []byte) ([]byte, error)
}

type localTransport struct {
	codec    codec
	handlers map[string]func(*request) (*response, error)
	raw      map[string][]byte
	errs     map[string]error
	maxResp  int
}

func newLocalTransport(c codec, maxResp int) *localTransport {
	return &localTransport{
		codec:    c,
		handlers: make(map[string]func(*request) (*response, error)),
		raw:      make(map[string][]byte),
		errs:     make(map[string]error),
		maxResp:  maxResp,
	}
}

func (t *localTransport) RoundTrip(service, method string, payload []byte) ([]byte, error) {
	key := service + "/" + method
	if e, ok := t.errs[key]; ok {
		return nil, e
	}
	if raw, ok := t.raw[key]; ok {
		return append([]byte(nil), raw...), nil
	}
	h, ok := t.handlers[key]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnimplemented, key)
	}
	req, err := t.codec.UnmarshalRequest(payload)
	if err != nil {
		return nil, fmt.Errorf("%w: server decode: %v", ErrInternal, err)
	}
	resp, err := h(req)
	if err != nil {
		return nil, err
	}
	out, err := t.codec.MarshalResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("%w: server encode: %v", ErrInternal, err)
	}
	if t.maxResp > 0 && len(out) > t.maxResp {
		return nil, fmt.Errorf("%w: server response %d > %d", ErrResourceExhausted, len(out), t.maxResp)
	}
	return out, nil
}

type statusError struct {
	Code int32
	Msg  string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("rpc error: code=%d desc=%s", e.Code, e.Msg)
}

type stub struct {
	service string
	t       transport
	codec   codec
	maxRecv int
}

func newStub(service string, t transport, c codec, maxRecv int) *stub {
	return &stub{service: service, t: t, codec: c, maxRecv: maxRecv}
}

// Invoke 为生成风格 client stub 的核心路径。
func (s *stub) Invoke(method string, req *request) (*response, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: nil request", ErrInternal)
	}
	payload, err := s.codec.MarshalRequest(req)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal request: %v", ErrInternal, err)
	}
	raw, err := s.t.RoundTrip(s.service, method, payload)
	if err != nil {
		return nil, err
	}
	if s.maxRecv > 0 && len(raw) > s.maxRecv {
		return nil, fmt.Errorf("%w: response %d > %d", ErrResourceExhausted, len(raw), s.maxRecv)
	}
	resp, err := s.codec.UnmarshalResponse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if resp.Seq != req.Seq {
		return nil, fmt.Errorf("%w: seq %d != %d", ErrInvalidResponse, resp.Seq, req.Seq)
	}
	if resp.Code != 0 {
		return resp, &statusError{Code: resp.Code, Msg: resp.Msg}
	}
	return resp, nil
}

func reverseBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i := range b {
		out[len(b)-1-i] = b[i]
	}
	return out
}

func setupTransport(c codec) *localTransport {
	t := newLocalTransport(c, 1<<20)
	t.handlers["svc/Echo"] = func(req *request) (*response, error) {
		return &response{Seq: req.Seq, Body: reverseBytes(req.Body)}, nil
	}
	t.handlers["svc/Fail"] = func(req *request) (*response, error) {
		return &response{Seq: req.Seq, Code: 13, Msg: "permission denied"}, nil
	}
	t.handlers["svc/Oversize"] = func(req *request) (*response, error) {
		return &response{Seq: req.Seq, Body: make([]byte, 4096)}, nil
	}
	t.handlers["svc/Malformed"] = func(req *request) (*response, error) {
		return &response{Seq: req.Seq}, nil
	}
	t.raw["svc/RawBad"] = []byte{0xff, 0x01}
	t.errs["svc/Reset"] = errTransport
	return t
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	torture := envInt("TORTURE_ROUNDS", 1)
	if torture > 100 {
		torture = 100
	}

	base := binaryCodec{}
	reg := newCodecRegistry()
	ck(reg.register(base) == nil, "register binary")
	ck(reg.register(base) == nil, "duplicate register allowed")
	names := reg.names()
	ck(len(names) == 1 && names[0] == "binary", "codec names dedup")
	got, err := reg.get("binary")
	ck(err == nil && got.Name() == "binary", "codec lookup")
	_, err = reg.get("missing")
	ck(errors.Is(err, ErrUnknownCodec), "unknown codec")
	ck(reg.register(nil) != nil, "nil codec rejected")

	t := setupTransport(base)
	st := newStub("svc", t, got, 1<<20)

	// 1. 固定往返：Echo 反转 body、seq 一致、deadline 保留。
	fixed := &request{Method: "Echo", Seq: 42, Body: []byte("hello-rpc"), Deadline: 12345}
	resp, err := st.Invoke("Echo", fixed)
	ck(err == nil, "fixed invoke")
	ck(resp != nil && resp.Seq == 42 && bytes.Equal(resp.Body, []byte("cpr-olleh")), "fixed echo response")
	gcSink = append(gcSink, fixed.Body, resp.Body)
	gcStress()
	ck(fixed.Seq == 42 && bytes.Equal(fixed.Body, []byte("hello-rpc")), "request survives GC")
	ck(bytes.Equal(resp.Body, []byte("cpr-olleh")), "response survives GC")

	// 2. 错误路径。
	_, err = st.Invoke("Missing", &request{Seq: 1})
	ck(errors.Is(err, ErrUnimplemented), "unimplemented method")
	_, err = st.Invoke("Fail", &request{Seq: 2})
	var se *statusError
	ck(errors.As(err, &se) && se.Code == 13 && se.Msg == "permission denied", "status error code")
	_, err = st.Invoke("Reset", &request{Seq: 3})
	ck(errors.Is(err, errTransport), "transport error propagated")

	smallStub := newStub("svc", t, got, 8)
	_, err = smallStub.Invoke("Oversize", &request{Seq: 4})
	ck(errors.Is(err, ErrResourceExhausted), "client maxRecv enforced")

	t.raw["svc/BadSeq"] = mustRaw(base, &response{Seq: 999, Body: []byte("x")})
	_, err = st.Invoke("BadSeq", &request{Seq: 5})
	ck(errors.Is(err, ErrInvalidResponse), "seq mismatch rejected")

	_, err = st.Invoke("RawBad", &request{Seq: 6})
	ck(errors.Is(err, ErrInvalidResponse), "malformed response rejected")

	_, err = st.Invoke("Echo", nil)
	ck(errors.Is(err, ErrInternal), "nil request rejected")

	// 3. 坏 codec：请求编码失败在客户端暴露，响应解码失败包装为 invalid。
	bad := &faultyCodec{base: base, failMarshal: true, failUnmarshal: true}
	stBad := newStub("svc", t, bad, 1<<20)
	_, err = stBad.Invoke("Echo", &request{Seq: 7})
	ck(errors.Is(err, ErrInternal), "marshal failure wrapped")
	bad.failMarshal = false
	_, err = stBad.Invoke("Echo", &request{Seq: 8})
	ck(errors.Is(err, ErrInvalidResponse), "unmarshal failure wrapped")

	// 4. 同名 codec 覆盖后 stub 使用新实现。
	override := &faultyCodec{base: base, name: "binary"}
	ck(reg.register(override) == nil, "override register")
	got2, _ := reg.get("binary")
	ck(got2 == override, "override wins")
	ck(len(reg.names()) == 1, "override keeps single name")
	st2 := newStub("svc", setupTransport(override), got2, 1<<20)
	r2, err := st2.Invoke("Echo", &request{Seq: 9, Body: []byte("abc")})
	ck(err == nil && bytes.Equal(r2.Body, []byte("cba")), "override codec used end-to-end")

	// 5. 编码器独立性：修改输入 body 不改变已编码 payload。
	reqA := &request{Method: "Echo", Seq: 1, Body: []byte{1, 2, 3}}
	encA, _ := base.MarshalRequest(reqA)
	reqA.Body[0] = 9
	decoded, err := base.UnmarshalRequest(encA)
	ck(err == nil && bytes.Equal(decoded.Body, []byte{1, 2, 3}), "marshal copies body")
	rawResp := mustRaw(base, &response{Seq: 1, Body: []byte{7, 8}})
	decResp, err := base.UnmarshalResponse(rawResp)
	rawResp[2] ^= 0xff
	ck(err == nil && bytes.Equal(decResp.Body, []byte{7, 8}), "unmarshal copies body")

	// 6. 注册表与 codec 在 GC 后仍可用。
	gcStress()
	got3, err := reg.get("binary")
	ck(err == nil && got3.Name() == "binary", "registry survives GC")
	ck(len(reg.names()) == 1, "names survives GC")

	// 7. 随机调用 × UAF_SEED；GC 穿插；保留请求/响应做 GC 后校验。
	r := rand.New(rand.NewSource(curSeed))
	type pair struct {
		req  *request
		resp *response
	}
	var kept []pair
	rounds := 300 * torture
	for iter := 0; iter < rounds; iter++ {
		body := make([]byte, r.Intn(200))
		for i := range body {
			body[i] = byte(r.Intn(256))
		}
		req := &request{Method: "Echo", Seq: uint64(r.Int63()), Body: body, Deadline: int64(r.Intn(1000))}
		resp, err := st.Invoke("Echo", req)
		ckSeed(err == nil, "random invoke")
		if err == nil {
			ckSeed(resp.Seq == req.Seq, "random seq")
			ckSeed(bytes.Equal(resp.Body, reverseBytes(body)), "random echo body")
			if iter%23 == 0 {
				kept = append(kept, pair{req: req, resp: resp})
				gcSink = append(gcSink, body, resp.Body)
				gcStress()
			}
		}
	}
	gcStress()
	for i, p := range kept {
		if p.resp.Seq != p.req.Seq || !bytes.Equal(p.resp.Body, reverseBytes(p.req.Body)) {
			ckSeed(false, fmt.Sprintf("kept rpc %d after GC", i))
			break
		}
	}

	finish("l10_service_stub")
}

func mustRaw(c codec, r *response) []byte {
	b, err := c.MarshalResponse(r)
	if err != nil {
		panic(err)
	}
	return b
}
