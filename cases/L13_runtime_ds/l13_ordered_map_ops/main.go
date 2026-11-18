// L13: 有序 map：二分查找定位、插入/更新/删除后 key 有序、前缀区间与随机操作参考模型。
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
)

type omRec struct {
	id  int
	key string
	val int
	pad [8]byte
}

type orderedMap struct {
	keys []string
	m    map[string]*omRec
}

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

func envSeed() int64 {
	s := os.Getenv("UAF_SEED")
	if s == "" {
		return 1
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 1
	}
	return v
}

func tortureRounds(def int) int {
	s := os.Getenv("TORTURE_ROUNDS")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return def
	}
	if n > 20 {
		n = 20
	}
	return n
}

var longPrefix = strings.Repeat("p", 1024)

func makeKey(id int) string {
	return fmt.Sprintf("%s/%03d/%s/%05d", longPrefix, id%17, strings.Repeat("x", 16), id)
}

func groupPrefix(g int) string {
	return fmt.Sprintf("%s/%03d/", longPrefix, g)
}

func newRec(id, val int) *omRec {
	r := &omRec{id: id, key: makeKey(id), val: val}
	for i := range r.pad {
		r.pad[i] = byte(id*11 + val*3 + i)
	}
	return r
}

func (o *orderedMap) search(k string) (int, bool) {
	i := sort.SearchStrings(o.keys, k)
	return i, i < len(o.keys) && o.keys[i] == k
}

func (o *orderedMap) put(rec *omRec) bool {
	i, found := o.search(rec.key)
	if found {
		o.m[rec.key] = rec
		return false
	}
	o.keys = append(o.keys, "")
	copy(o.keys[i+1:], o.keys[i:])
	o.keys[i] = rec.key
	o.m[rec.key] = rec
	return true
}

func (o *orderedMap) get(k string) (*omRec, bool) {
	_, found := o.search(k)
	if !found {
		return nil, false
	}
	r, ok := o.m[k]
	return r, ok
}

func (o *orderedMap) del(k string) bool {
	i, found := o.search(k)
	if !found {
		return false
	}
	copy(o.keys[i:], o.keys[i+1:])
	o.keys[len(o.keys)-1] = ""
	o.keys = o.keys[:len(o.keys)-1]
	delete(o.m, k)
	return true
}

func (o *orderedMap) prefixCount(prefix string) int {
	lo := sort.SearchStrings(o.keys, prefix)
	n := 0
	for i := lo; i < len(o.keys) && strings.HasPrefix(o.keys[i], prefix); i++ {
		n++
	}
	return n
}

func validRec(r *omRec, id, val int) bool {
	if r == nil || r.id != id || r.key != makeKey(id) || r.val != val {
		return false
	}
	for i := range r.pad {
		if r.pad[i] != byte(id*11+val*3+i) {
			return false
		}
	}
	return true
}

func (o *orderedMap) validate(tag string) {
	if len(o.keys) != len(o.m) {
		ckSeed(false, tag+": keys len != map len")
	}
	for i := 1; i < len(o.keys); i++ {
		if !(o.keys[i-1] < o.keys[i]) {
			ckSeed(false, fmt.Sprintf("%s: key order broken at %d", tag, i))
			break
		}
	}
	for _, k := range o.keys {
		if _, ok := o.m[k]; !ok {
			ckSeed(false, tag+": key without value")
			break
		}
	}
}

func main() {
	curSeed = envSeed()
	rounds := tortureRounds(3)

	for round := 0; round < rounds; round++ {
		r := rand.New(rand.NewSource(curSeed + int64(round)*3571))
		o := &orderedMap{m: make(map[string]*omRec, 320)}
		model := make(map[int]int, 320)
		const idSpace = 700

		// 1) 乱序插入 300 个长公共前缀 key。
		for _, id := range r.Perm(300) {
			val := id*3 + round
			o.put(newRec(id, val))
			model[id] = val
		}
		o.validate("ordered: initial")
		ck(len(o.keys) == 300 && len(model) == 300, "ordered: initial size")
		expected := make([]string, 0, 300)
		idsSorted := make([]int, 0, 300)
		for id := range model {
			idsSorted = append(idsSorted, id)
		}
		sort.Ints(idsSorted)
		for _, id := range idsSorted {
			expected = append(expected, makeKey(id))
		}
		sort.Strings(expected)
		for i := range expected {
			if o.keys[i] != expected[i] {
				ckSeed(false, fmt.Sprintf("ordered: sorted key mismatch at %d", i))
				break
			}
		}
		gcStress()
		o.validate("ordered: initial after GC")

		// 2) 随机 put/update/delete/get/prefix 操作，与 map[int]int 模型对照。
		for op := 0; op < 3000; op++ {
			id := r.Intn(idSpace)
			switch op % 6 {
			case 0, 1:
				val := op*7 + round
				o.put(newRec(id, val))
				model[id] = val
			case 2:
				val := op*13 + 1
				if _, ok := model[id]; ok {
					o.put(newRec(id, val))
					model[id] = val
				}
			case 3:
				deleted := o.del(makeKey(id))
				_, inModel := model[id]
				ckSeed(deleted == inModel, fmt.Sprintf("ordered: delete mismatch id=%d", id))
				delete(model, id)
			case 4:
				rec, ok := o.get(makeKey(id))
				val, inModel := model[id]
				ckSeed(ok == inModel, fmt.Sprintf("ordered: get presence mismatch id=%d", id))
				if ok {
					ckSeed(validRec(rec, id, val), fmt.Sprintf("ordered: get value mismatch id=%d", id))
				}
			case 5:
				g := id % 17
				gp := groupPrefix(g)
				want := 0
				for mid := range model {
					if mid%17 == g {
						want++
					}
				}
				got := o.prefixCount(gp)
				ckSeed(got == want, fmt.Sprintf("ordered: prefix count g=%d", g))
			}
			if op%400 == 399 {
				o.validate(fmt.Sprintf("ordered: op %d", op))
				ckSeed(len(o.keys) == len(model), fmt.Sprintf("ordered: size mismatch op=%d", op))
				for mid, mval := range model {
					rec, ok := o.get(makeKey(mid))
					ckSeed(ok && validRec(rec, mid, mval), fmt.Sprintf("ordered: model mismatch id=%d op=%d", mid, op))
				}
				gcStress()
				o.validate(fmt.Sprintf("ordered: op %d after GC", op))
			}
		}
		o.validate("ordered: random final")
		ck(len(o.keys) == len(model), "ordered: final size")

		// 3) 按顺序消费并删除前半，剩余顺序不变。
		half := len(o.keys) / 2
		firstHalf := append([]string(nil), o.keys[:half]...)
		for _, k := range firstHalf {
			if !o.del(k) {
				ckSeed(false, "ordered: delete existing key returned false")
				break
			}
			id := 0
			fmt.Sscanf(k[len(k)-5:], "%05d", &id)
			delete(model, id)
		}
		ck(len(o.keys) == len(model), "ordered: size after front delete")
		for i := 1; i < len(o.keys); i++ {
			if !(o.keys[i-1] < o.keys[i]) {
				ckSeed(false, "ordered: order broken after front delete")
				break
			}
		}
		gcStress()
		for _, k := range o.keys {
			id := 0
			fmt.Sscanf(k[len(k)-5:], "%05d", &id)
			val, ok := model[id]
			ckSeed(ok, "ordered: remaining key missing from model")
			rec, ok2 := o.get(k)
			ckSeed(ok2 && validRec(rec, id, val), fmt.Sprintf("ordered: remaining rec bad id=%d", id))
		}

		// 4) 反向清空：从尾部逐个删除，最终空结构可继续插入。
		for len(o.keys) > 0 {
			k := o.keys[len(o.keys)-1]
			id := 0
			fmt.Sscanf(k[len(k)-5:], "%05d", &id)
			ckSeed(o.del(k), "ordered: tail delete failed")
			delete(model, id)
			if len(o.keys)%97 == 0 {
				gcStress()
			}
		}
		ck(len(o.keys) == 0 && len(o.m) == 0 && len(model) == 0, "ordered: not empty after drain")
		gcStress()
		o.validate("ordered: empty")
		probe := newRec(699, 42)
		ck(o.put(probe), "ordered: insert into drained map failed")
		rec, ok := o.get(probe.key)
		ck(ok && validRec(rec, 699, 42), "ordered: lookup after drain reinsert")
		gcStress()
		ck(validRec(rec, 699, 42), "ordered: probe corrupted after GC")

		if intra := atomic.LoadInt32(&failed); intra != 0 {
			break
		}
		runtime.KeepAlive(o)
		runtime.KeepAlive(model)
		runtime.KeepAlive(expected)
	}

	finish("l13_ordered_map_ops")
}
