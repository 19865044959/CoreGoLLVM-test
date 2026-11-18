// L10: 生成风格固定大小字段编解码：64 字节 little-endian 记录与 32 字节
// big-endian 小记录，含 magic/位标志/定长 ID/保留区/CRC，边界与 GC。
// P2：TORTURE_ROUNDS 放大随机记录轮次。
package main

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"math/rand"
	"os"
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

var magic = [4]byte{'F', 'X', '0', '1'}

const (
	recordSize = 64
	knownFlags = uint16(0x0007)
)

type record struct {
	Version uint16
	Flags   uint16
	Count   uint64
	Delta   int64
	Ratio   float64
	ID      [16]byte
}

func (r *record) Length() int { return recordSize }

func (r *record) encode() ([recordSize]byte, error) {
	var b [recordSize]byte
	if r == nil {
		return b, fmt.Errorf("fixed codec: nil record")
	}
	if r.Version < 1 || r.Version > 3 {
		return b, fmt.Errorf("fixed codec: unsupported version %d", r.Version)
	}
	if r.Flags&^knownFlags != 0 {
		return b, fmt.Errorf("fixed codec: unknown flags 0x%04x", r.Flags)
	}
	copy(b[0:4], magic[:])
	binary.LittleEndian.PutUint16(b[4:6], r.Version)
	binary.LittleEndian.PutUint16(b[6:8], r.Flags)
	binary.LittleEndian.PutUint64(b[8:16], r.Count)
	binary.LittleEndian.PutUint64(b[16:24], uint64(r.Delta))
	binary.LittleEndian.PutUint64(b[24:32], math.Float64bits(r.Ratio))
	copy(b[32:48], r.ID[:])
	// 48..60 为保留区，encode 必须写零。
	binary.LittleEndian.PutUint32(b[60:64], crc32.ChecksumIEEE(b[:60]))
	return b, nil
}

func decodeRecord(b []byte) (*record, error) {
	if len(b) != recordSize {
		return nil, fmt.Errorf("fixed codec: length %d != %d", len(b), recordSize)
	}
	if string(b[0:4]) != string(magic[:]) {
		return nil, fmt.Errorf("fixed codec: bad magic")
	}
	ver := binary.LittleEndian.Uint16(b[4:6])
	if ver < 1 || ver > 3 {
		return nil, fmt.Errorf("fixed codec: unsupported version %d", ver)
	}
	flags := binary.LittleEndian.Uint16(b[6:8])
	if flags&^knownFlags != 0 {
		return nil, fmt.Errorf("fixed codec: unknown flags 0x%04x", flags)
	}
	for i := 48; i < 60; i++ {
		if b[i] != 0 {
			return nil, fmt.Errorf("fixed codec: reserved byte %d nonzero", i)
		}
	}
	want := crc32.ChecksumIEEE(b[:60])
	if got := binary.LittleEndian.Uint32(b[60:64]); got != want {
		return nil, fmt.Errorf("fixed codec: crc mismatch")
	}
	r := &record{
		Version: ver,
		Flags:   flags,
		Count:   binary.LittleEndian.Uint64(b[8:16]),
		Delta:   int64(binary.LittleEndian.Uint64(b[16:24])),
		Ratio:   math.Float64frombits(binary.LittleEndian.Uint64(b[24:32])),
	}
	copy(r.ID[:], b[32:48])
	return r, nil
}

// ---- 32 字节 big-endian 小记录（生成器另一套固定布局） ----

var tinyMagic = [4]byte{'T', 'N', 'Y', '1'}

const tinySize = 32

type tinyRecord struct {
	Count uint32
	Flags uint32
	ID    [8]byte
}

func crc16ccitt(b []byte) uint16 {
	crc := uint16(0xffff)
	for _, x := range b {
		crc ^= uint16(x) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

func (r *tinyRecord) Length() int { return tinySize }

func (r *tinyRecord) encode() ([tinySize]byte, error) {
	var b [tinySize]byte
	if r == nil {
		return b, fmt.Errorf("tiny codec: nil record")
	}
	if r.Flags&^uint32(0x0007) != 0 {
		return b, fmt.Errorf("tiny codec: unknown flags 0x%x", r.Flags)
	}
	copy(b[0:4], tinyMagic[:])
	binary.BigEndian.PutUint32(b[4:8], r.Count)
	binary.BigEndian.PutUint32(b[8:12], r.Flags)
	copy(b[12:20], r.ID[:])
	binary.BigEndian.PutUint16(b[30:32], crc16ccitt(b[:30]))
	return b, nil
}

func decodeTiny(b []byte) (*tinyRecord, error) {
	if len(b) != tinySize {
		return nil, fmt.Errorf("tiny codec: bad length")
	}
	if string(b[0:4]) != string(tinyMagic[:]) {
		return nil, fmt.Errorf("tiny codec: bad magic")
	}
	for i := 20; i < 30; i++ {
		if b[i] != 0 {
			return nil, fmt.Errorf("tiny codec: reserved nonzero")
		}
	}
	if binary.BigEndian.Uint16(b[30:32]) != crc16ccitt(b[:30]) {
		return nil, fmt.Errorf("tiny codec: crc16 mismatch")
	}
	r := &tinyRecord{
		Count: binary.BigEndian.Uint32(b[4:8]),
		Flags: binary.BigEndian.Uint32(b[8:12]),
	}
	copy(r.ID[:], b[12:20])
	return r, nil
}

func fixRecordCRC(b [recordSize]byte) [recordSize]byte {
	binary.LittleEndian.PutUint32(b[60:64], crc32.ChecksumIEEE(b[:60]))
	return b
}

func sameRecord(a, b *record) bool {
	return a != nil && b != nil && a.Version == b.Version && a.Flags == b.Flags &&
		a.Count == b.Count && a.Delta == b.Delta &&
		math.Float64bits(a.Ratio) == math.Float64bits(b.Ratio) && a.ID == b.ID
}

func main() {
	curSeed = 1
	if s := os.Getenv("UAF_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			curSeed = v
		}
	}
	rounds := envInt("TORTURE_ROUNDS", 1)
	if rounds > 100 {
		rounds = 100
	}

	// 1. golden 字节：字段偏移、端序、CRC。
	var id [16]byte
	for i := range id {
		id[i] = byte(i)
	}
	rec := &record{Version: 1, Flags: 0x0005, Count: 0x0102030405060708, Delta: -2, Ratio: 1.5, ID: id}
	enc, err := rec.encode()
	ck(err == nil, "record encode")
	ck(fmt.Sprintf("%x", enc) == "46583031010005000807060504030201feffffffffffffff000000000000f83f000102030405060708090a0b0c0d0e0f000000000000000000000000237b683e",
		"record golden bytes")
	ck(rec.Length() == 64, "record length")
	var tid [8]byte
	for i := range tid {
		tid[i] = byte(i)
	}
	tr := &tinyRecord{Count: 0x01020304, Flags: 5, ID: tid}
	tenc, err := tr.encode()
	ck(err == nil, "tiny encode")
	ck(fmt.Sprintf("%x", tenc) == "544e593101020304000000050001020304050607000000000000000000009109",
		"tiny golden bytes")
	ck(tr.Length() == 32, "tiny length")

	// 2. roundtrip 与输入缓冲独立。
	dec, err := decodeRecord(enc[:])
	ck(err == nil && sameRecord(rec, dec), "record roundtrip")
	tdec, err := decodeTiny(tenc[:])
	ck(err == nil && tdec.Count == tr.Count && tdec.Flags == tr.Flags && tdec.ID == tr.ID, "tiny roundtrip")
	for i := range enc {
		enc[i] = 0xaa
	}
	ck(dec != nil && dec.Count == rec.Count && dec.ID == id && dec.Delta == -2, "decode copies out of input buffer")
	ck(dec.Ratio == 1.5 && dec.Version == 1 && dec.Flags == 5, "decoded scalar fields")

	// 3. 极值与位模式往返（NaN/Inf 用位比较）。
	nan := math.Float64frombits(0x7ff8000000000001)
	extreme := &record{Version: 3, Flags: 7, Count: ^uint64(0), Delta: math.MinInt64, Ratio: nan, ID: id}
	eb, err := extreme.encode()
	ck(err == nil, "extreme record encode")
	ed, err := decodeRecord(eb[:])
	ck(err == nil && sameRecord(extreme, ed), "extreme record roundtrip (NaN bits)")
	inf := &record{Version: 2, Flags: 0, Count: 0, Delta: math.MaxInt64, Ratio: math.Inf(-1)}
	ib, err := inf.encode()
	ck(err == nil, "inf record encode")
	id2, err := decodeRecord(ib[:])
	ck(err == nil && math.IsInf(id2.Ratio, -1) && id2.Delta == math.MaxInt64, "inf record roundtrip")

	// 4. 截断/坏 magic/坏版本/未知 flags/保留区/坏 CRC。
	for n := 0; n < recordSize; n++ {
		if _, err := decodeRecord(eb[:n]); err == nil {
			ck(false, fmt.Sprintf("record truncation %d accepted", n))
			break
		}
	}
	tooLong := append(eb[:], 0)
	if _, err := decodeRecord(tooLong); err == nil {
		ck(false, "66-byte record accepted")
	}
	badMagic := eb
	badMagic[0] = 'X'
	if _, err := decodeRecord(badMagic[:]); err == nil {
		ck(false, "bad magic accepted")
	}
	badVer := eb
	badVer[4] = 4
	badVer[5] = 0
	badVer = fixRecordCRC(badVer)
	if _, err := decodeRecord(badVer[:]); err == nil {
		ck(false, "bad version accepted")
	}
	badFlags := eb
	badFlags[6] = 8
	badFlags[7] = 0
	badFlags = fixRecordCRC(badFlags)
	if _, err := decodeRecord(badFlags[:]); err == nil {
		ck(false, "unknown flags accepted")
	}
	badReserved := eb
	badReserved[48] = 1
	badReserved = fixRecordCRC(badReserved)
	if _, err := decodeRecord(badReserved[:]); err == nil {
		ck(false, "nonzero reserved accepted")
	}
	badCRC := eb
	badCRC[60] ^= 1
	if _, err := decodeRecord(badCRC[:]); err == nil {
		ck(false, "bad crc accepted")
	}
	badEnc := &record{Version: 0, Flags: 0}
	if _, err := badEnc.encode(); err == nil {
		ck(false, "encode version 0 accepted")
	}
	badEnc = &record{Version: 1, Flags: 0x10}
	if _, err := badEnc.encode(); err == nil {
		ck(false, "encode unknown flags accepted")
	}
	if _, err := (*record)(nil).encode(); err == nil {
		ck(false, "encode nil record accepted")
	}
	for n := 0; n < tinySize; n++ {
		if _, err := decodeTiny(tenc[:n]); err == nil {
			ck(false, fmt.Sprintf("tiny truncation %d accepted", n))
			break
		}
	}
	tbad := tenc
	tbad[20] = 1
	if _, err := decodeTiny(tbad[:]); err == nil {
		ck(false, "tiny reserved accepted")
	}
	tbad2 := tenc
	tbad2[31] ^= 1
	if _, err := decodeTiny(tbad2[:]); err == nil {
		ck(false, "tiny bad crc accepted")
	}

	// 5. GC：保留编码缓冲与记录，GC 后重新解码。
	goldenBackup := make([]byte, recordSize)
	for i := range eb {
		goldenBackup[i] = eb[i]
	}
	gcSink = append(gcSink, goldenBackup, []byte{1, 2, 3})
	gcStress()
	gd, err := decodeRecord(goldenBackup)
	ck(err == nil && sameRecord(extreme, gd), "record decodes after GC")
	ck(gd != nil && gd.ID == id, "ID array survives GC")
	gin := append([]byte(nil), tenc[:]...)
	gcStress()
	gt, err := decodeTiny(gin)
	ck(err == nil && gt.Count == tr.Count && gt.ID == tr.ID, "tiny decodes after GC")

	// 6. UAF_SEED 随机记录 × TORTURE_ROUNDS。
	r := rand.New(rand.NewSource(curSeed))
	for i := 0; i < 150*rounds; i++ {
		src := &record{
			Version: uint16(1 + r.Intn(3)),
			Flags:   uint16(r.Intn(8)),
			Count:   r.Uint64(),
			Delta:   int64(r.Uint64()),
			Ratio:   math.Float64frombits(r.Uint64()),
		}
		for j := range src.ID {
			src.ID[j] = byte(r.Intn(256))
		}
		b, err := src.encode()
		ckSeed(err == nil, fmt.Sprintf("round %d record encode", i))
		if err != nil {
			continue
		}
		out, err := decodeRecord(b[:])
		ckSeed(err == nil && sameRecord(src, out), fmt.Sprintf("round %d record roundtrip", i))
		b2, err2 := out.encode()
		ckSeed(err2 == nil && b2 == b, fmt.Sprintf("round %d record deterministic re-encode", i))
		if i%29 == 0 {
			gcSink = append(gcSink, b[:])
			gcStress()
			out2, err := decodeRecord(b[:])
			ckSeed(err == nil && sameRecord(src, out2), fmt.Sprintf("round %d record after GC", i))
		}
	}
	for i := 0; i < 100*rounds; i++ {
		src := &tinyRecord{Count: r.Uint32(), Flags: uint32(r.Intn(4))}
		for j := range src.ID {
			src.ID[j] = byte(r.Intn(256))
		}
		b, err := src.encode()
		ckSeed(err == nil, fmt.Sprintf("round %d tiny encode", i))
		if err != nil {
			continue
		}
		out, err := decodeTiny(b[:])
		ckSeed(err == nil && out.Count == src.Count && out.Flags == src.Flags && out.ID == src.ID,
			fmt.Sprintf("round %d tiny roundtrip", i))
		if i%37 == 0 {
			gcStress()
			out2, err := decodeTiny(b[:])
			ckSeed(err == nil && out2.Count == src.Count && out2.ID == src.ID,
				fmt.Sprintf("round %d tiny after GC", i))
		}
	}

	finish("l10_fixed_size_codec")
}
