// L10: 生成风格 service stub：方法描述符自带默认 codec，请求可协商 binary/json
// codec；覆盖 golden 字节、注册表覆盖、错误路径、缓冲拷贝与 GC。
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
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
	ErrUnknownCodec      = errors.New("rpc: unknown codec")
	ErrNilRequest        = errors.New("rpc: nil request")
	ErrResourceExhausted = errors.New("rpc: resource exhausted")
	ErrInternal          = errors.New("rpc: internal")
	ErrInvalidResponse   = errors.New("rpc: invalid response")
	errTransport         = errors.New("transport: broken pipe")
	errServerDecode      = errors.New("server: decode failed")
)

type statusError struct {
	Code int32
	Msg  string
}

func (e *statusError) Error() string { return fmt.Sprintf("rpc status %d: %s", e.Code, e.Msg) }

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

// ---- 生成风格消息与 codec ----

type request struct {
	Method string   `json:"method"`
	Seq    uint64   `json:"seq"`
	Body   []byte   `json:"body"`
	Tags   []string `json:"tags"`
}

type response struct {
	Seq  uint64 `json:"seq"`
	Code int32  `json:"code"`
	Body []byte `json:"body"`
	Msg  string `json:"msg"`
}

type codec interface {
	Name() string
	Marshal(v any) ([]byte, error)
	Unmarshal(b []byte, v any) error
}

type binReader struct {
	b   []byte
	off int
	err error
}

func (r *binReader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.b)-r.off {
		r.err = errors.New("binary codec: truncated")
		return nil
	}
	out := r.b[r.off : r.off+n]
	r.off += n
	return out
}

func (r *binReader) u8() uint8 {
	b := r.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *binReader) u16() uint16 {
	b := r.take(2)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}

func (r *binReader) u32() uint32 {
	b := r.take(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *binReader) u64() uint64 {
	b := r.take(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func (r *binReader) str() string { return string(r.take(int(r.u16()))) }
func (r *binReader) blob() []byte {
	b := r.take(int(r.u32()))
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

type binaryCodec struct{}

func (binaryCodec) Name() string { return "binary" }

func appendU16(b []byte, v uint16) []byte { return append(b, byte(v>>8), byte(v)) }
func appendU32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
func appendU64(b []byte, v uint64) []byte {
	return append(b, byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
		byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (binaryCodec) Marshal(v any) ([]byte, error) {
	switch m := v.(type) {
	case *request:
		if m == nil {
			return nil, ErrNilRequest
		}
		b := []byte{1}
		b = appendU16(b, uint16(len(m.Method)))
		b = append(b, m.Method...)
		b = appendU64(b, m.Seq)
		b = appendU32(b, uint32(len(m.Body)))
		b = append(b, m.Body...)
		b = appendU16(b, uint16(len(m.Tags)))
		for _, t := range m.Tags {
			b = appendU16(b, uint16(len(t)))
			b = append(b, t...)
		}
		return b, nil
	case *response:
		if m == nil {
			return nil, errors.New("binary codec: nil response")
		}
		b := []byte{2}
		b = appendU64(b, m.Seq)
		b = appendU32(b, uint32(m.Code))
		b = appendU16(b, uint16(len(m.Msg)))
		b = append(b, m.Msg...)
		b = appendU32(b, uint32(len(m.Body)))
		b = append(b, m.Body...)
		return b, nil
	default:
		return nil, fmt.Errorf("binary codec: unsupported %T", v)
	}
}

func (binaryCodec) Unmarshal(b []byte, v any) error {
	r := &binReader{b: b}
	ver := r.u8()
	switch m := v.(type) {
	case *request:
		if ver != 1 {
			return fmt.Errorf("binary codec: bad request version %d", ver)
		}
		var out request
		out.Method = r.str()
		out.Seq = r.u64()
		out.Body = r.blob()
		n := int(r.u16())
		if r.err == nil {
			out.Tags = make([]string, 0, n)
			for i := 0; i < n && r.err == nil; i++ {
				out.Tags = append(out.Tags, r.str())
			}
		}
		if r.err != nil {
			return r.err
		}
		if r.off != len(r.b) {
			return errors.New("binary codec: trailing bytes")
		}
		*m = out
		return nil
	case *response:
		if ver != 2 {
			return fmt.Errorf("binary codec: bad response version %d", ver)
		}
		var out response
		out.Seq = r.u64()
		out.Code = int32(r.u32())
		out.Msg = r.str()
		out.Body = r.blob()
		if r.err != nil {
			return r.err
		}
		if r.off != len(r.b) {
			return errors.New("binary codec: trailing bytes")
		}
		*m = out
		return nil
	default:
		return fmt.Errorf("binary codec: unsupported %T", v)
	}
}

type jsonCodec struct{}

func (jsonCodec) Name() string { return "json" }
func (jsonCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}
func (jsonCodec) Unmarshal(b []byte, v any) error {
	return json.Unmarshal(b, v)
}

type badCodec struct{}

func (badCodec) Name() string                  { return "bad" }
func (badCodec) Marshal(any) ([]byte, error)   { return nil, errors.New("bad codec: marshal") }
func (badCodec) Unmarshal([]byte, any) error   { return errors.New("bad codec: unmarshal") }

// countingJSON 用于验证同名 codec 覆盖后 stub 立即使用新实现。
type countingJSON struct {
	n     int
	inner codec
}

func (c *countingJSON) Name() string { return "json" }
func (c *countingJSON) Marshal(v any) ([]byte, error) {
	c.n++
	return c.inner.Marshal(v)
}
func (c *countingJSON) Unmarshal(b []byte, v any) error {
	c.n++
	return c.inner.Unmarshal(b, v)
}

var codecRegistry = map[string]codec{
	"binary": binaryCodec{},
	"json":   jsonCodec{},
	"bad":    badCodec{},
}

func registerCodec(c codec) {
	if c == nil || c.Name() == "" {
		return
	}
	codecRegistry[c.Name()] = c
}

func getCodec(name string) (codec, bool) {
	c, ok := codecRegistry[name]
	return c, ok
}

func codecNames() []string {
	out := make([]string, 0, len(codecRegistry))
	for k := range codecRegistry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ---- 生成风格 method descriptor 与 handler ----

type methodDesc struct {
	Name    string
	Codec   string
	Handler func(*request) (*response, error)
}

func reverseBody(req *request) (*response, error) {
	body := make([]byte, len(req.Body))
	for i := range req.Body {
		body[len(req.Body)-1-i] = req.Body[i]
	}
	return &response{Seq: req.Seq, Body: body, Msg: "ok"}, nil
}

func upperBody(req *request) (*response, error) {
	return &response{Seq: req.Seq, Body: bytes.ToUpper(req.Body), Msg: "up"}, nil
}

func failBody(req *request) (*response, error) {
	return nil, &statusError{Code: 13, Msg: "boom"}
}

var serviceMethods = map[string]methodDesc{
	"echo":  {Name: "echo", Codec: "binary", Handler: reverseBody},
	"upper": {Name: "upper", Codec: "json", Handler: upperBody},
	"fail":  {Name: "fail", Codec: "binary", Handler: failBody},
}

func methodNames() []string {
	out := make([]string, 0, len(serviceMethods))
	for k := range serviceMethods {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type server struct {
	forceCodec string
	seqBias    uint64
	transport  bool
}

func (s *server) handle(method, codecName string, frame []byte) ([]byte, error) {
	if s.transport {
		return nil, errTransport
	}
	if s.forceCodec != "" {
		codecName = s.forceCodec
	}
	c, ok := getCodec(codecName)
	if !ok {
		return nil, errServerDecode
	}
	var req request
	if err := c.Unmarshal(frame, &req); err != nil {
		return nil, errServerDecode
	}
	d, ok := serviceMethods[method]
	if !ok {
		return nil, errServerDecode
	}
	resp, err := d.Handler(&req)
	if err != nil {
		var se *statusError
		if errors.As(err, &se) {
			resp = &response{Code: se.Code, Msg: se.Msg}
		} else {
			resp = &response{Code: 13, Msg: "handler error"}
		}
	}
	if resp == nil {
		resp = &response{Code: 13, Msg: "empty response"}
	}
	resp.Seq = req.Seq + s.seqBias
	return c.Marshal(resp)
}

type client struct {
	srv     *server
	maxRecv int
}

func (c *client) Invoke(method string, req *request, codecOverride string) (*response, error) {
	d, ok := serviceMethods[method]
	if !ok {
		return nil, ErrUnimplemented
	}
	if req == nil {
		return nil, ErrNilRequest
	}
	name := codecOverride
	if name == "" {
		name = d.Codec
	}
	cd, ok := getCodec(name)
	if !ok {
		return nil, ErrUnknownCodec
	}
	frame, err := cd.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}
	raw, err := c.srv.handle(method, name, frame)
	if err != nil {
		if errors.Is(err, errTransport) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if c.maxRecv > 0 && len(raw) > c.maxRecv {
		return nil, ErrResourceExhausted
	}
	var resp response
	if err := cd.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidResponse, err)
	}
	if resp.Seq != req.Seq {
		return nil, fmt.Errorf("%w: seq %d != %d", ErrInvalidResponse, resp.Seq, req.Seq)
	}
	if resp.Code != 0 {
		return nil, &statusError{Code: resp.Code, Msg: resp.Msg}
	}
	return &resp, nil
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}

	// 1. 方法/注册表结构。
	ck(fmt.Sprint(methodNames()) == "[echo fail upper]", "sorted method names")
	ck(fmt.Sprint(codecNames()) == "[bad binary json]", "sorted codec names")
	ck(serviceMethods["echo"].Codec == "binary" && serviceMethods["upper"].Codec == "json",
		"per-method default codec")
	firstResp, firstErr := serviceMethods["echo"].Handler(&request{})
	ck(firstErr == nil && firstResp != nil && len(firstResp.Body) == 0, "handler table callable")

	// 2. golden 编码：binary 请求/响应、JSON 请求。
	req := &request{Method: "echo", Seq: 7, Body: []byte{0xde, 0xad, 0x01}, Tags: []string{"a", "bc"}}
	bin, err := binaryCodec{}.Marshal(req)
	ck(err == nil, "binary request marshal")
	ck(fmt.Sprintf("%x", bin) == "0100046563686f000000000000000700000003dead01000200016100026263",
		"binary request golden bytes")
	respGolden := &response{Seq: 7, Code: 0, Msg: "ok", Body: []byte{1, 2}}
	rb, err := binaryCodec{}.Marshal(respGolden)
	ck(err == nil, "binary response marshal")
	ck(fmt.Sprintf("%x", rb) == "0200000000000000070000000000026f6b000000020102",
		"binary response golden bytes")
	jb, err := jsonCodec{}.Marshal(req)
	ck(err == nil, "json request marshal")
	ck(string(jb) == `{"method":"echo","seq":7,"body":"3q0B","tags":["a","bc"]}`, "json request golden")
	var reqBack request
	ck(binaryCodec{}.Unmarshal(bin, &reqBack) == nil, "binary request unmarshal")
	ck(reqBack.Method == "echo" && reqBack.Seq == 7 && bytes.Equal(reqBack.Body, req.Body) &&
		fmt.Sprint(reqBack.Tags) == "[a bc]", "binary request roundtrip")
	// 解码器必须拷贝 body：改写输入缓冲后消息不变。
	for i := range bin {
		bin[i] = 0xff
	}
	ck(bytes.Equal(reqBack.Body, []byte{0xde, 0xad, 0x01}) && reqBack.Method == "echo",
		"binary decode copies payload out of input buffer")

	// 3. 默认 codec 与协商覆盖：echo 默认 binary、upper 默认 json；强制覆盖后仍往返。
	cl := &client{srv: &server{}}
	r1, err := cl.Invoke("echo", req, "")
	ck(err == nil && bytes.Equal(r1.Body, []byte{0x01, 0xad, 0xde}) && r1.Msg == "ok" && r1.Seq == 7,
		"echo default binary roundtrip")
	r2, err := cl.Invoke("upper", &request{Seq: 8, Body: []byte("ab-z"), Tags: []string{"t"}}, "")
	ck(err == nil && string(r2.Body) == "AB-Z" && r2.Seq == 8, "upper default json roundtrip")
	r3, err := cl.Invoke("upper", &request{Seq: 9, Body: []byte("mix")}, "binary")
	ck(err == nil && string(r3.Body) == "MIX", "upper with binary override")
	r4, err := cl.Invoke("echo", &request{Seq: 10, Body: []byte("json")}, "json")
	ck(err == nil && string(r4.Body) == "nosj", "echo with json override")

	// 4. 同名 codec 覆盖：名称列表不变，后续 json 调用走新实现。
	cj := &countingJSON{inner: jsonCodec{}}
	registerCodec(cj)
	ck(fmt.Sprint(codecNames()) == "[bad binary json]", "duplicate codec name deduped")
	got, _ := getCodec("json")
	ck(got == codec(cj), "codec registry last registration wins")
	before := cj.n
	_, err = cl.Invoke("upper", &request{Seq: 11, Body: []byte("count")}, "")
	ck(err == nil && cj.n > before, "stub uses overridden codec")
	registerCodec(jsonCodec{}) // 恢复，避免影响随机段
	ck(fmt.Sprint(codecNames()) == "[bad binary json]", "restore json codec keeps single name")

	// 5. 错误路径。
	if _, err := cl.Invoke("nope", req, ""); !errors.Is(err, ErrUnimplemented) {
		ck(false, "unknown method -> ErrUnimplemented")
	}
	if _, err := cl.Invoke("echo", nil, ""); !errors.Is(err, ErrNilRequest) {
		ck(false, "nil request -> ErrNilRequest")
	}
	if _, err := cl.Invoke("echo", req, "missing"); !errors.Is(err, ErrUnknownCodec) {
		ck(false, "unknown codec -> ErrUnknownCodec")
	}
	if _, err := cl.Invoke("echo", req, "json"); err != nil {
		ck(false, "explicit json codec accepted")
	}
	if _, err := cl.Invoke("echo", req, "bad"); !errors.Is(err, ErrInternal) {
		ck(false, "bad codec marshal -> ErrInternal")
	}
	_, err = cl.Invoke("fail", &request{Seq: 12}, "")
	var se *statusError
	ck(errors.As(err, &se) && se.Code == 13 && se.Msg == "boom", "handler status -> statusError")
	clTransport := &client{srv: &server{transport: true}}
	if _, err := clTransport.Invoke("echo", req, ""); !errors.Is(err, errTransport) {
		ck(false, "transport error passthrough")
	}
	clSmall := &client{srv: &server{}, maxRecv: 8}
	if _, err := clSmall.Invoke("upper", &request{Seq: 13, Body: []byte("0123456789")}, ""); !errors.Is(err, ErrResourceExhausted) {
		ck(false, "maxRecv -> ErrResourceExhausted")
	}
	clSeq := &client{srv: &server{seqBias: 5}}
	if _, err := clSeq.Invoke("echo", req, ""); !errors.Is(err, ErrInvalidResponse) {
		ck(false, "seq mismatch -> ErrInvalidResponse")
	}
	// 客户端用 json 解码，服务端强制 binary 编码：响应解码失败。
	clMismatch := &client{srv: &server{forceCodec: "binary"}}
	if _, err := clMismatch.Invoke("echo", req, "json"); !errors.Is(err, ErrInvalidResponse) {
		ck(false, "codec mismatch -> ErrInvalidResponse")
	}
	// 服务端收到无法解码的请求：返回 invalid response。
	registerCodec(truncCodec{})
	if _, err := cl.Invoke("echo", req, "trunc"); !errors.Is(err, ErrInvalidResponse) {
		ck(false, "truncated request -> ErrInvalidResponse")
	}
	delete(codecRegistry, "trunc")
	ck(fmt.Sprint(codecNames()) == "[bad binary json]", "trunc codec removed")

	// 6. GC：保留响应、codec 名称表与注册项，GC 后继续可用。
	befName := fmt.Sprint(codecNames())
	retained := []*response{r1, r2, r3, r4}
	gcSink = append(gcSink, r1.Body, r2.Body, r3.Body)
	gcStress()
	ck(fmt.Sprint(codecNames()) == befName, "codec names stable across GC")
	ck(bytes.Equal(retained[0].Body, []byte{0x01, 0xad, 0xde}) && string(retained[1].Body) == "AB-Z",
		"responses survive GC")
	r5, err := cl.Invoke("echo", &request{Seq: 14, Body: []byte("post-gc")}, "")
	ck(err == nil && string(r5.Body) == "cg-tsop", "stub works after GC")

	// 7. UAF_SEED 随机调用：请求体/seq/tags/codec 随机，echo/upper 结果与独立模型一致。
	r := rand.New(rand.NewSource(curSeed))
	for i := 0; i < 260; i++ {
		body := make([]byte, r.Intn(48))
		for j := range body {
			body[j] = byte(r.Intn(256))
		}
		tags := make([]string, r.Intn(4))
		for j := range tags {
			tags[j] = fmt.Sprintf("t%d", r.Intn(1000))
		}
		rr := &request{Seq: r.Uint64(), Body: body, Tags: tags}
		method := "echo"
		if r.Intn(2) == 0 {
			method = "upper"
		}
		codecName := ""
		switch r.Intn(4) {
		case 0:
			codecName = "binary"
		case 1:
			codecName = "json"
		}
		got, err := cl.Invoke(method, rr, codecName)
		ckSeed(err == nil, fmt.Sprintf("round %d invoke %s codec=%q", i, method, codecName))
		if err != nil {
			continue
		}
		var want []byte
		if method == "echo" {
			want = make([]byte, len(body))
			for j := range body {
				want[len(body)-1-j] = body[j]
			}
		} else {
			want = bytes.ToUpper(body)
		}
		ckSeed(got.Seq == rr.Seq && bytes.Equal(got.Body, want),
			fmt.Sprintf("round %d %s body/seq", i, method))
		if r.Intn(3) == 0 {
			gcSink = append(gcSink, got.Body)
			gcStress()
			again, err := cl.Invoke(method, rr, codecName)
			ckSeed(err == nil && bytes.Equal(again.Body, want),
				fmt.Sprintf("round %d result after GC", i))
			ckSeed(bytes.Equal(got.Body, want), fmt.Sprintf("round %d retained response after GC", i))
		}
		if r.Intn(17) == 0 {
			_, err := cl.Invoke("fail", rr, codecName)
			var se2 *statusError
			ckSeed(errors.As(err, &se2) && se2.Code == 13,
				fmt.Sprintf("round %d fail status", i))
		}
	}

	finish("l10_service_codec_stub")
}

// truncCodec 生成合法名称但始终让服务端解码失败，用于覆盖 server decode 分支。
type truncCodec struct{}

func (truncCodec) Name() string { return "trunc" }
func (truncCodec) Marshal(v any) ([]byte, error) {
	return []byte{0xff, 0xff}, nil
}
func (truncCodec) Unmarshal(b []byte, v any) error {
	// 客户端侧编码不会被调用；服务端强制选用本 codec 时必然失败。
	return errors.New("trunc codec cannot decode")
}
