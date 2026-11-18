// L01: 泛型 CSV 编解码：分隔符探测（逗号/分号/制表符）、引号/换行/空字段往返与错误路径。
// 对应 gitea modules/csv 的 delimiter 猜测与 gitdiff CSV 表格解析。
package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

type csvPerson struct {
	name string
	role string
	age  int64
	note string
	tags []string
}

type csvPoint struct {
	x, y  int64
	label string
}

type Codec[T any] struct {
	Header     []string
	ToRecord   func(T) []string
	FromRecord func([]string) (T, error)
}

func detectDelimiter(sample string) rune {
	cands := []rune{',', ';', '\t'}
	counts := [3]int{}
	quoted := false
	for i := 0; i < len(sample); i++ {
		c := sample[i]
		if c == '\n' && !quoted {
			break
		}
		if c == '"' {
			if quoted && i+1 < len(sample) && sample[i+1] == '"' {
				i++
				continue
			}
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		for j, d := range cands {
			if rune(c) == d {
				counts[j]++
			}
		}
	}
	best := 0
	for j := 1; j < len(cands); j++ {
		if counts[j] > counts[best] {
			best = j
		}
	}
	return cands[best]
}

func (c Codec[T]) Encode(w io.Writer, rows []T, delim rune) error {
	cw := csv.NewWriter(w)
	cw.Comma = delim
	if err := cw.Write(c.Header); err != nil {
		return err
	}
	for i, row := range rows {
		rec := c.ToRecord(row)
		if len(rec) != len(c.Header) {
			return fmt.Errorf("row %d field count got=%d want=%d", i, len(rec), len(c.Header))
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func (c Codec[T]) DecodeWithDelimiter(r io.Reader, delim rune) ([]T, error) {
	cr := csv.NewReader(r)
	cr.Comma = delim
	cr.FieldsPerRecord = len(c.Header)
	cr.TrimLeadingSpace = false
	hdr, err := cr.Read()
	if err != nil {
		return nil, err
	}
	if len(hdr) != len(c.Header) {
		return nil, fmt.Errorf("header field count got=%d want=%d", len(hdr), len(c.Header))
	}
	for i := range hdr {
		if hdr[i] != c.Header[i] {
			return nil, fmt.Errorf("header[%d] got=%q want=%q", i, hdr[i], c.Header[i])
		}
	}
	var out []T
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		v, err := c.FromRecord(rec)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (c Codec[T]) Decode(r io.Reader) ([]T, rune, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	delim := detectDelimiter(string(data))
	out, err := c.DecodeWithDelimiter(bytes.NewReader(data), delim)
	return out, delim, err
}

var (
	failed  int32
	gcSink  [][]byte
	uafSeed int64 = 1
	roles         = []string{"dev", "ops", "qa", "sre,on-call", "data"}
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 16; i++ {
		b := make([]byte, 2048)
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
	n, err := strconv.ParseInt(os.Getenv("UAF_SEED"), 10, 64)
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func personCodec() Codec[csvPerson] {
	return Codec[csvPerson]{
		Header: []string{"name", "role", "age", "note", "tags"},
		ToRecord: func(p csvPerson) []string {
			return []string{p.name, p.role, strconv.FormatInt(p.age, 10), p.note, strings.Join(p.tags, "|")}
		},
		FromRecord: func(rec []string) (csvPerson, error) {
			age, err := strconv.ParseInt(rec[2], 10, 64)
			if err != nil {
				return csvPerson{}, err
			}
			var tags []string
			if rec[4] != "" {
				tags = strings.Split(rec[4], "|")
			}
			return csvPerson{name: rec[0], role: rec[1], age: age, note: rec[3], tags: tags}, nil
		},
	}
}

func pointCodec() Codec[csvPoint] {
	return Codec[csvPoint]{
		Header: []string{"x", "y", "label"},
		ToRecord: func(p csvPoint) []string {
			return []string{strconv.FormatInt(p.x, 10), strconv.FormatInt(p.y, 10), p.label}
		},
		FromRecord: func(rec []string) (csvPoint, error) {
			x, err := strconv.ParseInt(rec[0], 10, 64)
			if err != nil {
				return csvPoint{}, err
			}
			y, err := strconv.ParseInt(rec[1], 10, 64)
			if err != nil {
				return csvPoint{}, err
			}
			return csvPoint{x: x, y: y, label: rec[2]}, nil
		},
	}
}

func samePerson(a, b csvPerson) bool {
	if a.name != b.name || a.role != b.role || a.age != b.age || a.note != b.note {
		return false
	}
	return strings.Join(a.tags, "|") == strings.Join(b.tags, "|")
}

func runRoundTrip(seed int64) {
	n := 20 + int(seed%12)
	rows := make([]csvPerson, n)
	for i := 0; i < n; i++ {
		note := fmt.Sprintf("note-%d,%s", i, roles[i%len(roles)])
		switch i % 5 {
		case 0:
			note = "quoted \"inner\" text, with comma"
		case 1:
			note = "line1\nline2, after newline"
		case 2:
			note = ""
		}
		var tags []string
		for j := 0; j < i%4; j++ {
			tags = append(tags, fmt.Sprintf("t%d", j))
		}
		rows[i] = csvPerson{
			name: fmt.Sprintf("user-%03d", i),
			role: roles[i%len(roles)],
			age:  int64(18 + (i*7)%50),
			note: note,
			tags: tags,
		}
	}

	var buf bytes.Buffer
	codec := personCodec()
	ck(codec.Encode(&buf, rows, ',') == nil, "encode comma CSV failed")
	gcStress()
	got, delim, err := codec.Decode(bytes.NewReader(append([]byte(nil), buf.Bytes()...)))
	ck(err == nil, fmt.Sprintf("decode comma CSV failed: %v", err))
	ck(delim == ',', fmt.Sprintf("delimiter detection got=%q want=','", delim))
	ck(len(got) == len(rows), fmt.Sprintf("row count got=%d want=%d", len(got), len(rows)))
	for i := 0; i < len(got) && i < len(rows); i++ {
		ck(samePerson(got[i], rows[i]), fmt.Sprintf("row %d roundtrip mismatch", i))
	}

	// 编码结果再解码两次：显式 GC 后字符串/切片内容必须保持。
	gcStress()
	again, _, err := codec.Decode(bytes.NewReader(append([]byte(nil), buf.Bytes()...)))
	ck(err == nil && len(again) == len(rows), "second decode failed")
	if err == nil {
		for i := 0; i < len(again); i += 3 {
			ck(samePerson(again[i], rows[i]), fmt.Sprintf("post-GC row %d mismatch", i))
		}
	}
}

func runSemicolonAndTab() {
	// 分号分隔 + 引号内分号 + 空字段 + CRLF。
	semi := "name;role;age;note;tags\r\n" +
		"alice;dev;30;\"semi;colon\";x|y\r\n" +
		"bob;;0;;\r\n" +
		"carol;qa;41;plain;z\r\n"
	codec := personCodec()
	rows, delim, err := codec.Decode(strings.NewReader(semi))
	ck(err == nil, fmt.Sprintf("semicolon decode failed: %v", err))
	ck(delim == ';', fmt.Sprintf("semicolon detection got=%q", delim))
	ck(len(rows) == 3, fmt.Sprintf("semicolon row count got=%d", len(rows)))
	if len(rows) == 3 {
		ck(rows[0].name == "alice" && rows[0].note == "semi;colon" && strings.Join(rows[0].tags, "|") == "x|y", "semicolon row0 mismatch")
		ck(rows[1].name == "bob" && rows[1].role == "" && rows[1].age == 0 && rows[1].note == "" && len(rows[1].tags) == 0, "semicolon empty fields mismatch")
		ck(rows[2].note == "plain", "semicolon row2 mismatch")
	}

	// 制表符分隔，字段内包含逗号，不应误判为逗号 CSV。
	tab := "name\trole\tage\tnote\ttags\n" +
		"dave\tops\t28\t\"a,b,c\"\tt1|t2\n" +
		"erin\tsre,on-call\t35\t\t\n"
	rows, delim, err = codec.Decode(strings.NewReader(tab))
	ck(err == nil, fmt.Sprintf("tab decode failed: %v", err))
	ck(delim == '\t', fmt.Sprintf("tab detection got=%q", delim))
	ck(len(rows) == 2, fmt.Sprintf("tab row count got=%d", len(rows)))
	if len(rows) == 2 {
		ck(rows[0].note == "a,b,c" && rows[0].role == "ops", "tab row0 mismatch")
		ck(rows[1].role == "sre,on-call" && rows[1].note == "", "tab row1 mismatch")
	}

	// 第二种 T 实例化：分号分隔的点表。
	points := []csvPoint{{1, -2, "left"}, {300, 40, "right;top"}, {0, 0, ""}}
	var buf bytes.Buffer
	pc := pointCodec()
	ck(pc.Encode(&buf, points, ';') == nil, "point encode failed")
	gcStress()
	got, delim, err := pc.Decode(bytes.NewReader(buf.Bytes()))
	ck(err == nil, fmt.Sprintf("point decode failed: %v", err))
	ck(delim == ';', fmt.Sprintf("point delimiter got=%q", delim))
	ck(len(got) == 3, "point row count mismatch")
	for i := 0; i < len(got) && i < len(points); i++ {
		ck(got[i] == points[i], fmt.Sprintf("point row %d mismatch got=%+v want=%+v", i, got[i], points[i]))
	}
}

func runErrors() {
	codec := personCodec()
	errCount := 0

	// 1) 编码时字段数不匹配。
	badEncode := Codec[csvPerson]{
		Header:   []string{"a", "b", "c"},
		ToRecord: func(csvPerson) []string { return []string{"only", "two"} },
	}
	var buf bytes.Buffer
	if badEncode.Encode(&buf, []csvPerson{{}}, ',') != nil {
		errCount++
	}

	// 2) 解码字段数不匹配。
	badRows := "name,role,age,note,tags\nx,y,1\n"
	if _, _, err := codec.Decode(strings.NewReader(badRows)); err != nil {
		errCount++
	}

	// 3) 数字字段非法。
	badAge := "name,role,age,note,tags\nx,y,not-an-int,,\n"
	if _, _, err := codec.Decode(strings.NewReader(badAge)); err != nil {
		errCount++
	}

	// 4) header 不匹配。
	badHeader := "other,role,age,note,tags\nx,y,1,,\n"
	if _, _, err := codec.Decode(strings.NewReader(badHeader)); err != nil {
		errCount++
	}

	ck(errCount == 4, fmt.Sprintf("error path count got=%d want=4", errCount))
}

func main() {
	uafSeed = envSeed()
	runRoundTrip(uafSeed)
	runSemicolonAndTab()
	runErrors()
	finish("l01_generic_csv_codec")
}
