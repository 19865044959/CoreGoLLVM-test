// N12: 固定 seed 变异语料 + 多解码器长跑。
// 启动时自生成 JSON/XML/gob/gzip/tar/zip/png 语料；每个 worker 用 xorshift 变异器
// （字节翻转/随机写/截断/插入/删除/片段复制/跨语料拼接）循环变异，输入上限 64KB：
//   - 每个输入 recover panic 并分类计数（panic 率只记录，不断言）；
//   - 每次解码有结构预算：JSON/XML 嵌套深度、gzip 解压字节、tar/zip 条目数与解包字节、
//     PNG IHDR 尺寸；每输入只解码一次，watchdog 检测死循环/卡死；
//   - 成功解码后对 JSON/gob 做 roundtrip 抽查（marshal/encode 后再解码并逐值比较）；
//   - 独立 GC churn 与保留环穿插，制造解码结果的对象图压力。
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const scenario = "n12"

const (
	maxInput     = 64 << 10
	jsonMaxDepth = 128
	xmlMaxDepth  = 128
	gzipBudget   = 512 << 10
	tarBudget    = 512 << 10
	zipBudget    = 512 << 10
	zipMaxFiles  = 64
	tarMaxFiles  = 64
	pngMaxPixels = 1 << 20
)

const (
	kindJSON = iota
	kindXML
	kindGob
	kindGzip
	kindTar
	kindZip
	kindPNG
	numKinds
)

var kindNames = [numKinds]string{"json", "xml", "gob", "gzip", "tar", "zip", "png"}

var (
	beats       int64
	gcCycles    int64
	iterations  int64
	decodes     [numKinds]int64
	oks         [numKinds]int64
	errs        [numKinds]int64
	panics      [numKinds]int64
	skips       [numKinds]int64
	roundtrips  int64
	retainedMax int64
)

var (
	errSkip   = fmt.Errorf("skip: structural budget")
	errBudget = fmt.Errorf("budget exceeded")
)

// gobPayload 只含定长数组/字符串/基本类型：变异输入无法诱导无界的 slice/map 分配。
type gobInner struct {
	Name  string
	Score int64
	Tags  [4]string
	Flags [8]uint8
	Ratio float64
}

type gobPayload struct {
	ID     int64
	Title  string
	Inner  gobInner
	Values [16]int64
	Blob   [32]byte
}

type xmlItem struct {
	XMLName xml.Name `xml:"item"`
	ID      int      `xml:"id,attr"`
	Value   string   `xml:"value"`
}

type xmlRoot struct {
	XMLName xml.Name  `xml:"root"`
	Name    string    `xml:"name"`
	Items   []xmlItem `xml:"items>item"`
	Note    string    `xml:"note"`
}

type corpus struct {
	data [numKinds][][]byte
}

type worker struct {
	id       int64
	seed     uint64
	rng      uint64
	cur      [numKinds][]byte
	retain   [2][]byte
	iter     int64
	iterKind [numKinds]int64
}

func failf(format string, args ...interface{}) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	buf := make([]byte, 4<<20)
	n := runtime.Stack(buf, true)
	fmt.Fprintf(os.Stderr, "%s", buf[:n])
	os.Exit(1)
}

func envInt(key string, def, min, max int) int {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < min {
		return def
	}
	if v > max {
		return max
	}
	return v
}

func envSeed(def uint64) uint64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return def
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v == 0 {
		return def
	}
	return v
}

func setupCrashOutput() {
	p := os.Getenv("STRESS_CRASH_FILE")
	if p == "" {
		return
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_ = debug.SetCrashOutput(f, debug.CrashOptions{})
}

func (w *worker) next() uint64 {
	x := w.rng
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	w.rng = x
	return x
}

func buildCorpus() *corpus {
	c := new(corpus)

	// JSON
	jsonDoc := map[string]interface{}{
		"id":     int64(42),
		"name":   "n12-corpus",
		"active": true,
		"ratio":  1.25,
		"vals":   []interface{}{int64(1), int64(2), int64(3), int64(4), int64(5)},
		"nested": map[string]interface{}{
			"a": "b",
			"c": []interface{}{"x", "y", map[string]interface{}{"z": 9.5}},
			"deep": []interface{}{map[string]interface{}{
				"k1": []interface{}{int64(7), "s"},
				"k2": map[string]interface{}{"k3": true, "k4": nil},
			}},
		},
		"text": string(bytes.Repeat([]byte("lorem ipsum "), 64)),
	}
	if b, err := json.MarshalIndent(jsonDoc, "", "  "); err == nil {
		c.data[kindJSON] = append(c.data[kindJSON], b)
	}
	jsonArr := []interface{}{int64(1), "two", []interface{}{int64(3), 4.5, nil}, map[string]interface{}{"ok": true}}
	if b, err := json.Marshal(jsonArr); err == nil {
		c.data[kindJSON] = append(c.data[kindJSON], b)
	}

	// XML
	root := xmlRoot{
		Name: "n12",
		Items: []xmlItem{
			{ID: 1, Value: "alpha"},
			{ID: 2, Value: string(bytes.Repeat([]byte("beta "), 32))},
			{ID: 3, Value: "<escaped & é>"},
		},
		Note: "deterministic xml corpus",
	}
	if b, err := xml.MarshalIndent(root, "", "  "); err == nil {
		c.data[kindXML] = append(c.data[kindXML], b)
	}
	if b, err := xml.Marshal(root); err == nil {
		c.data[kindXML] = append(c.data[kindXML], b)
	}

	// gob
	payload := gobPayload{
		ID:    42,
		Title: "n12-gob",
		Inner: gobInner{Name: "inner", Score: -7, Tags: [4]string{"a", "b", "c", "d"}, Ratio: 3.5},
	}
	for i := range payload.Values {
		payload.Values[i] = int64(i*i - 5)
	}
	for i := range payload.Blob {
		payload.Blob[i] = byte(i * 3)
	}
	var gbuf bytes.Buffer
	if err := gob.NewEncoder(&gbuf).Encode(payload); err == nil {
		c.data[kindGob] = append(c.data[kindGob], gbuf.Bytes())
	}
	payload.ID = -1
	payload.Title = string(bytes.Repeat([]byte("gob "), 64))
	payload.Inner.Tags[0] = "mutated"
	if err := gob.NewEncoder(&gbuf).Encode(payload); err == nil {
		c.data[kindGob] = append(c.data[kindGob], gbuf.Bytes())
	}

	// gzip（压缩 JSON 语料）
	if len(c.data[kindJSON]) > 0 {
		var zbuf bytes.Buffer
		zw := gzip.NewWriter(&zbuf)
		_, _ = zw.Write(c.data[kindJSON][0])
		_ = zw.Close()
		c.data[kindGzip] = append(c.data[kindGzip], zbuf.Bytes())
	}
	{
		var zbuf bytes.Buffer
		zw := gzip.NewWriter(&zbuf)
		_, _ = zw.Write(bytes.Repeat([]byte("compressible payload "), 128))
		_ = zw.Close()
		c.data[kindGzip] = append(c.data[kindGzip], zbuf.Bytes())
	}

	// tar
	{
		var tbuf bytes.Buffer
		tw := tar.NewWriter(&tbuf)
		for i := 0; i < 3; i++ {
			body := bytes.Repeat([]byte(fmt.Sprintf("file-%d-", i)), 64)
			hdr := &tar.Header{Name: fmt.Sprintf("dir/file%d.txt", i), Mode: 0o644, Size: int64(len(body))}
			if err := tw.WriteHeader(hdr); err == nil {
				_, _ = tw.Write(body)
			}
		}
		_ = tw.Close()
		c.data[kindTar] = append(c.data[kindTar], tbuf.Bytes())
	}

	// zip
	{
		var zbuf bytes.Buffer
		zw := zip.NewWriter(&zbuf)
		for i := 0; i < 3; i++ {
			fw, err := zw.Create(fmt.Sprintf("file%d.json", i))
			if err == nil {
				_, _ = fw.Write(c.data[kindJSON][0])
			}
		}
		_ = zw.Close()
		c.data[kindZip] = append(c.data[kindZip], zbuf.Bytes())
	}

	// png
	{
		img := image.NewRGBA(image.Rect(0, 0, 32, 32))
		for y := 0; y < 32; y++ {
			for x := 0; x < 32; x++ {
				img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: uint8((x + y) * 4), A: 255})
			}
		}
		var pbuf bytes.Buffer
		if err := png.Encode(&pbuf, img); err == nil {
			c.data[kindPNG] = append(c.data[kindPNG], pbuf.Bytes())
		}
	}

	for k := 0; k < numKinds; k++ {
		if len(c.data[k]) == 0 {
			failf("scenario=%s phase=corpus: empty corpus for %s", scenario, kindNames[k])
		}
		for _, b := range c.data[k] {
			if len(b) > maxInput {
				failf("scenario=%s phase=corpus: %s corpus %d > %d", scenario, kindNames[k], len(b), maxInput)
			}
		}
	}
	return c
}

// mutate 执行 1-4 个变异操作，结果始终复制且不超过 maxInput。
func mutate(x *worker, base, other []byte, seed []byte) []byte {
	out := append([]byte(nil), base...)
	nops := 1 + int(x.next()%4)
	for i := 0; i < nops; i++ {
		op := x.next() % 7
		switch op {
		case 0: // 字节翻转
			if len(out) > 0 {
				k := 1 + int(x.next()%8)
				for j := 0; j < k && len(out) > 0; j++ {
					p := int(x.next() % uint64(len(out)))
					out[p] ^= byte(1 << (x.next() % 8))
				}
			}
		case 1: // 随机覆写
			if len(out) > 0 {
				k := 1 + int(x.next()%16)
				for j := 0; j < k && len(out) > 0; j++ {
					p := int(x.next() % uint64(len(out)))
					out[p] = byte(x.next())
				}
			}
		case 2: // 截断
			if len(out) > 0 {
				out = out[:int(x.next()%uint64(len(out)+1))]
			}
		case 3: // 插入随机字节
			pos := int(x.next() % uint64(len(out)+1))
			k := 1 + int(x.next()%16)
			ins := make([]byte, k)
			for j := range ins {
				ins[j] = byte(x.next())
			}
			nout := make([]byte, 0, len(out)+k)
			nout = append(nout, out[:pos]...)
			nout = append(nout, ins...)
			nout = append(nout, out[pos:]...)
			out = nout
		case 4: // 删除片段
			if len(out) > 1 {
				pos := int(x.next() % uint64(len(out)))
				k := 1 + int(x.next()%16)
				if pos+k > len(out) {
					k = len(out) - pos
				}
				out = append(out[:pos], out[pos+k:]...)
			}
		case 5: // 片段复制
			if len(out) > 1 {
				from := int(x.next() % uint64(len(out)))
				k := 1 + int(x.next()%16)
				if from+k > len(out) {
					k = len(out) - from
				}
				seg := append([]byte(nil), out[from:from+k]...)
				pos := int(x.next() % uint64(len(out)+1))
				nout := make([]byte, 0, len(out)+len(seg))
				nout = append(nout, out[:pos]...)
				nout = append(nout, seg...)
				nout = append(nout, out[pos:]...)
				out = nout
			}
		case 6: // 跨语料拼接（含初始语料）
			var donor []byte
			if x.next()%2 == 0 && len(other) > 0 {
				donor = other
			} else {
				donor = seed
			}
			if len(donor) > 0 {
				cut := int(x.next() % uint64(len(out)+1))
				dcut := int(x.next() % uint64(len(donor)+1))
				nout := make([]byte, 0, cut+len(donor)-dcut)
				nout = append(nout, out[:cut]...)
				nout = append(nout, donor[dcut:]...)
				out = nout
			}
		}
		if len(out) > maxInput {
			out = out[:maxInput]
		}
		if x.next()%32 == 0 {
			break
		}
	}
	if len(out) > maxInput {
		out = out[:maxInput]
	}
	return out
}

func jsonDepthOK(b []byte) bool {
	depth := 0
	inStr := false
	esc := false
	for _, c := range b {
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
			if depth > jsonMaxDepth {
				return false
			}
		case '}', ']':
			depth--
		}
	}
	return true
}

func decodeJSON(data []byte) error {
	if !jsonDepthOK(data) {
		return errSkip
	}
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	b2, err := json.Marshal(v)
	if err != nil {
		failf("scenario=%s decoder=json seed=%d: marshal after successful decode: %v", scenario, 0, err)
	}
	var v2 interface{}
	if err := json.Unmarshal(b2, &v2); err != nil {
		failf("scenario=%s decoder=json: re-unmarshal of marshaled value: %v", scenario, err)
	}
	if !reflect.DeepEqual(v, v2) {
		failf("scenario=%s decoder=json: roundtrip mismatch decoded=%T remarshal=%T", scenario, v, v2)
	}
	atomic.AddInt64(&roundtrips, 1)
	return nil
}

func decodeXML(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	depth := 0
	tokens := 0
	maxTokens := len(data)*2 + 1024
	for tokens < maxTokens {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		tokens++
		switch tok.(type) {
		case xml.StartElement:
			depth++
			if depth > xmlMaxDepth {
				return errSkip
			}
		case xml.EndElement:
			depth--
			if depth < 0 {
				return fmt.Errorf("xml negative depth")
			}
		}
	}
	if tokens >= maxTokens {
		return errBudget
	}
	if depth != 0 {
		return fmt.Errorf("xml unbalanced depth=%d", depth)
	}
	return nil
}

func decodeGob(data []byte) error {
	var p gobPayload
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&p); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(p); err != nil {
		failf("scenario=%s decoder=gob: re-encode decoded value: %v", scenario, err)
	}
	var p2 gobPayload
	if err := gob.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&p2); err != nil {
		failf("scenario=%s decoder=gob: decode re-encoded value: %v", scenario, err)
	}
	if p != p2 {
		failf("scenario=%s decoder=gob: roundtrip mismatch", scenario)
	}
	atomic.AddInt64(&roundtrips, 1)
	return nil
}

func decodeGzip(data []byte) error {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer r.Close()
	n, err := io.CopyN(io.Discard, r, gzipBudget+1)
	if n > gzipBudget {
		return errBudget
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return nil
	}
	return err
}

func decodeTar(data []byte) error {
	tr := tar.NewReader(bytes.NewReader(data))
	entries := 0
	var total int64
	for entries < tarMaxFiles {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		entries++
		if hdr.Size < 0 {
			return errBudget
		}
		n := hdr.Size
		if n > tarBudget-total {
			n = tarBudget - total
		}
		if n > 0 {
			m, err := io.CopyN(io.Discard, tr, n)
			total += m
			if err != nil && err != io.EOF {
				return err
			}
			if total >= tarBudget {
				return errBudget
			}
		}
	}
	return errBudget
}

func decodeZip(data []byte) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	var total uint64
	for i, f := range zr.File {
		if i >= zipMaxFiles {
			return errBudget
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		limit := f.UncompressedSize64
		if limit > zipBudget-total {
			limit = zipBudget - total
		}
		n, err := io.CopyN(io.Discard, rc, int64(limit))
		_ = rc.Close()
		total += uint64(n)
		if err != nil && err != io.EOF {
			return err
		}
		if total >= zipBudget {
			return errBudget
		}
	}
	return nil
}

func decodePNG(data []byte) error {
	if len(data) >= 24 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		w := binary.BigEndian.Uint32(data[16:20])
		h := binary.BigEndian.Uint32(data[20:24])
		if w > 1024 || h > 1024 || uint64(w)*uint64(h) > pngMaxPixels {
			return errSkip
		}
	}
	_, err := png.Decode(bytes.NewReader(data))
	return err
}

func decodeByKind(kind int, data []byte) error {
	switch kind {
	case kindJSON:
		return decodeJSON(data)
	case kindXML:
		return decodeXML(data)
	case kindGob:
		return decodeGob(data)
	case kindGzip:
		return decodeGzip(data)
	case kindTar:
		return decodeTar(data)
	case kindZip:
		return decodeZip(data)
	case kindPNG:
		return decodePNG(data)
	}
	return fmt.Errorf("unknown kind %d", kind)
}

func safeDecode(kind int, data []byte) (err error, panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			err = nil
		}
	}()
	err = decodeByKind(kind, data)
	return err, false
}

func (w *worker) decodeOne(kind int, c *corpus) {
	base := w.cur[kind]
	other := w.cur[(kind+1)%numKinds]
	seed := c.data[kind][w.next()%uint64(len(c.data[kind]))]
	w.iterKind[kind]++
	var data []byte
	if w.iterKind[kind]%32 == 0 {
		// 周期性做一次未变异语料解码，保证每个 decoder 都有成功路径与 roundtrip 样本。
		data = append([]byte(nil), seed...)
	} else {
		data = mutate(w, base, other, seed)
	}
	w.cur[kind] = data
	if len(data) > int(atomic.LoadInt64(&retainedMax)) {
		atomic.StoreInt64(&retainedMax, int64(len(data)))
	}

	err, panicked := safeDecode(kind, data)
	if panicked {
		atomic.AddInt64(&panics[kind], 1)
	} else if err != nil {
		if err == errSkip {
			atomic.AddInt64(&skips[kind], 1)
		} else {
			atomic.AddInt64(&errs[kind], 1)
		}
	} else {
		atomic.AddInt64(&oks[kind], 1)
		// 保留近期输入，制造解码路径之外的对象存活压力。
		w.retain[w.iter%2] = append(w.retain[w.iter%2][:0], data...)
	}
	atomic.AddInt64(&decodes[kind], 1)
	atomic.AddInt64(&iterations, 1)
	atomic.AddInt64(&beats, 1)
	w.iter++

	// 周期性回到初始语料，避免长期漂移到恒定错误输入。
	if w.iterKind[kind]%16 == 0 {
		origin := c.data[kind][w.next()%uint64(len(c.data[kind]))]
		w.cur[kind] = append(w.cur[kind][:0], origin...)
	}
}

func (w *worker) run(deadline time.Time, seconds int, fixed int64, c *corpus) {
	r := int64(0)
	for {
		if seconds > 0 {
			if time.Now().After(deadline) {
				break
			}
		} else if r >= fixed {
			break
		}
		for i := 0; i < 32; i++ {
			kind := int(w.next() % uint64(numKinds))
			w.decodeOne(kind, c)
		}
		if r%8 == 0 {
			runtime.GC()
		}
		r++
	}
}

func gcChurn(stop <-chan struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	var sink [][]byte
	for {
		select {
		case <-stop:
			return
		default:
		}
		for i := 0; i < 8; i++ {
			b := make([]byte, 4096)
			b[0] = byte(i)
			sink = append(sink[:0], b)
		}
		for i := range sink {
			sink[i] = nil
		}
		sink = sink[:0]
		runtime.GC()
		atomic.AddInt64(&gcCycles, 1)
		time.Sleep(20 * time.Millisecond)
	}
}

func watchdog(interval time.Duration, stop <-chan struct{}) {
	last := atomic.LoadInt64(&beats)
	lastChange := time.Now()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			cur := atomic.LoadInt64(&beats)
			if cur != last {
				last = cur
				lastChange = time.Now()
				continue
			}
			if time.Since(lastChange) > 6*interval {
				failf("scenario=%s watchdog stalled: no decode progress for %s (iterations=%d): possible decoder hang",
					scenario, time.Since(lastChange), cur)
			}
		}
	}
}

func main() {
	setupCrashOutput()
	seconds := envInt("STRESS_SECONDS", 60, 0, 86400)
	workers := envInt("STRESS_WORKERS", 4, 1, 32)
	hb := envInt("STRESS_HEARTBEAT", 5, 1, 60)
	seed := envSeed(1)
	fixed := int64(envInt("STRESS_FIXED_ROUNDS", 24, 1, 100000))

	baseGoroutines := runtime.NumGoroutine()
	c := buildCorpus()
	corpusSizes := make([]int, numKinds)
	for k := 0; k < numKinds; k++ {
		corpusSizes[k] = len(c.data[k])
	}

	fmt.Printf("scenario=%s phase=preflight go=%s goos=%s goarch=%s gomaxprocs=%d seed=%d seconds=%d workers=%d fixed_rounds=%d heartbeat=%ds max_input=%d corpus_sizes=%v\n",
		scenario, runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), seed, seconds, workers, fixed, hb, maxInput, corpusSizes)

	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	ws := make([]*worker, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		w := &worker{id: int64(i), seed: seed, rng: seed*10000 + uint64(i)*7919 + 1}
		for k := 0; k < numKinds; k++ {
			origin := c.data[k][w.next()%uint64(len(c.data[k]))]
			w.cur[k] = append([]byte(nil), origin...)
		}
		ws[i] = w
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.run(deadline, seconds, fixed, c)
		}()
	}

	stop := make(chan struct{})
	var helpers sync.WaitGroup
	helpers.Add(2)
	go gcChurn(stop, &helpers)
	go func() {
		defer helpers.Done()
		watchdog(time.Duration(hb)*time.Second, stop)
	}()

	wg.Wait()
	close(stop)
	helpers.Wait()

	n := runtime.NumGoroutine()
	if n > baseGoroutines+2 {
		failf("scenario=%s phase=shutdown expected=goroutines<=%d actual=%d: goroutine leak",
			scenario, baseGoroutines+2, n)
	}

	total := int64(0)
	for k := 0; k < numKinds; k++ {
		total += atomic.LoadInt64(&decodes[k])
	}
	if total == 0 {
		failf("scenario=%s phase=final expected=\"decodes>0\" actual=0", scenario)
	}
	// 每个解码器都必须被实际喂过输入（固定轮数下也成立）。
	for k := 0; k < numKinds; k++ {
		if atomic.LoadInt64(&decodes[k]) == 0 {
			failf("scenario=%s phase=final expected=\"decoder %s exercised\" actual=0", scenario, kindNames[k])
		}
	}

	panicTotal := int64(0)
	for k := 0; k < numKinds; k++ {
		panicTotal += atomic.LoadInt64(&panics[k])
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	fmt.Printf("metrics: iterations=%d total_decodes=%d roundtrips=%d panic_total=%d panic_rate=%.6f skip_total=",
		atomic.LoadInt64(&iterations), total, atomic.LoadInt64(&roundtrips), panicTotal, float64(panicTotal)/float64(total))
	skipTotal := int64(0)
	for k := 0; k < numKinds; k++ {
		skipTotal += atomic.LoadInt64(&skips[k])
	}
	fmt.Printf("%d max_input_seen=%d gc_churn=%d beats=%d heap_alloc=%d heap_objects=%d num_gc=%d goroutines=%d\n",
		skipTotal, atomic.LoadInt64(&retainedMax), atomic.LoadInt64(&gcCycles), atomic.LoadInt64(&beats),
		m.HeapAlloc, m.HeapObjects, m.NumGC, n)
	for k := 0; k < numKinds; k++ {
		d := atomic.LoadInt64(&decodes[k])
		fmt.Printf("decoder=%s decodes=%d ok=%d err=%d panic=%d skip=%d panic_rate=%.6f\n",
			kindNames[k], d, atomic.LoadInt64(&oks[k]), atomic.LoadInt64(&errs[k]),
			atomic.LoadInt64(&panics[k]), atomic.LoadInt64(&skips[k]),
			float64(atomic.LoadInt64(&panics[k]))/float64(d))
	}
	fmt.Println("STRESS PASS n12_fuzz_decoder")
}
