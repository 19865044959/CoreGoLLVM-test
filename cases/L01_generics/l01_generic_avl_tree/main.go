// L01: 泛型 AVL 树：ordered 约束 + 泛型负载指针，插入/删除/旋转后校验
// BST 序、平衡因子、高度与跨 GC 指针身份。
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
)

type ordered interface {
	~int | ~int64 | ~string
}

type rec struct {
	id   int64
	name string
	blob []byte
}

type anode[K ordered, V any] struct {
	key         K
	val         *V
	left, right *anode[K, V]
	height      int
}

func hgt[K ordered, V any](n *anode[K, V]) int {
	if n == nil {
		return 0
	}
	return n.height
}

func upd[K ordered, V any](n *anode[K, V]) {
	l, r := hgt(n.left), hgt(n.right)
	if l > r {
		n.height = l + 1
	} else {
		n.height = r + 1
	}
}

func bal[K ordered, V any](n *anode[K, V]) int {
	if n == nil {
		return 0
	}
	return hgt(n.left) - hgt(n.right)
}

func rotR[K ordered, V any](y *anode[K, V]) *anode[K, V] {
	x := y.left
	t := x.right
	x.right = y
	y.left = t
	upd(y)
	upd(x)
	return x
}

func rotL[K ordered, V any](x *anode[K, V]) *anode[K, V] {
	y := x.right
	t := y.left
	y.left = x
	x.right = t
	upd(x)
	upd(y)
	return y
}

func rebalance[K ordered, V any](n *anode[K, V]) *anode[K, V] {
	upd(n)
	b := bal(n)
	if b > 1 {
		if bal(n.left) < 0 {
			n.left = rotL(n.left)
		}
		return rotR(n)
	}
	if b < -1 {
		if bal(n.right) > 0 {
			n.right = rotR(n.right)
		}
		return rotL(n)
	}
	return n
}

func insert[K ordered, V any](n *anode[K, V], k K, v *V) (*anode[K, V], *V, bool) {
	if n == nil {
		return &anode[K, V]{key: k, val: v, height: 1}, nil, false
	}
	var old *V
	var existed bool
	if k < n.key {
		n.left, old, existed = insert(n.left, k, v)
	} else if n.key < k {
		n.right, old, existed = insert(n.right, k, v)
	} else {
		old = n.val
		n.val = v
		return n, old, true
	}
	if !existed {
		return rebalance(n), nil, false
	}
	return n, old, true
}

func minNode[K ordered, V any](n *anode[K, V]) *anode[K, V] {
	for n.left != nil {
		n = n.left
	}
	return n
}

func remove[K ordered, V any](n *anode[K, V], k K) (*anode[K, V], *V, bool) {
	if n == nil {
		return nil, nil, false
	}
	if k < n.key {
		var old *V
		var ok bool
		n.left, old, ok = remove(n.left, k)
		if !ok {
			return n, nil, false
		}
		return rebalance(n), old, true
	}
	if n.key < k {
		var old *V
		var ok bool
		n.right, old, ok = remove(n.right, k)
		if !ok {
			return n, nil, false
		}
		return rebalance(n), old, true
	}
	old := n.val
	if n.left == nil {
		return n.right, old, true
	}
	if n.right == nil {
		return n.left, old, true
	}
	succ := minNode(n.right)
	n.key = succ.key
	n.val = succ.val
	n.right, _, _ = remove(n.right, succ.key)
	return rebalance(n), old, true
}

type AVL[K ordered, V any] struct {
	root *anode[K, V]
	n    int
}

func (t *AVL[K, V]) Insert(k K, v *V) (*V, bool) {
	newRoot, old, existed := insert(t.root, k, v)
	t.root = newRoot
	if !existed {
		t.n++
	}
	return old, existed
}

func (t *AVL[K, V]) Delete(k K) (*V, bool) {
	newRoot, old, ok := remove(t.root, k)
	t.root = newRoot
	if ok {
		t.n--
	}
	return old, ok
}

func (t *AVL[K, V]) Get(k K) (*V, bool) {
	n := t.root
	for n != nil {
		if k < n.key {
			n = n.left
		} else if n.key < k {
			n = n.right
		} else {
			return n.val, true
		}
	}
	return nil, false
}

func (t *AVL[K, V]) Len() int { return t.n }

func (t *AVL[K, V]) Height() int { return hgt(t.root) }

// Walk 按中序访问，遇到回调返回 false 提前停止。
func (t *AVL[K, V]) Walk(fn func(K, *V) bool) {
	var walk func(n *anode[K, V]) bool
	walk = func(n *anode[K, V]) bool {
		if n == nil {
			return true
		}
		if !walk(n.left) {
			return false
		}
		if !fn(n.key, n.val) {
			return false
		}
		return walk(n.right)
	}
	walk(t.root)
}

// Valid 校验 BST 序、每节点高度以及左右子树高度差不超过 1。
func (t *AVL[K, V]) Valid() bool {
	var prev K
	first := true
	orderedOK := true
	t.Walk(func(k K, _ *V) bool {
		if !first && !(prev < k) {
			orderedOK = false
			return false
		}
		prev = k
		first = false
		return true
	})
	if !orderedOK {
		return false
	}
	var check func(n *anode[K, V]) (int, int, bool)
	check = func(n *anode[K, V]) (int, int, bool) {
		if n == nil {
			return 0, 0, true
		}
		lh, lc, lok := check(n.left)
		rh, rc, rok := check(n.right)
		if !lok || !rok {
			return 0, 0, false
		}
		h := lh + 1
		if rh+1 > h {
			h = rh + 1
		}
		if n.height != h {
			return 0, 0, false
		}
		d := lh - rh
		if d < -1 || d > 1 {
			return 0, 0, false
		}
		return h, lc + rc + 1, true
	}
	_, count, ok := check(t.root)
	return ok && count == t.n
}

var (
	failed  int32
	gcSink  [][]byte
	recSink []*rec
	uafSeed int64 = 1
)

func ck(ok bool, msg string) {
	if !ok {
		atomic.AddInt32(&failed, 1)
		fmt.Println("FAIL:", msg)
	}
}

func gcStress() {
	for i := 0; i < 8; i++ {
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

func nextRand(state *uint64) uint64 {
	*state = *state*6364136223846793005 + 1442695040888963407
	return *state >> 33
}

func shuffle(items []int64, state *uint64) {
	for i := len(items) - 1; i > 0; i-- {
		j := int(nextRand(state) % uint64(i+1))
		items[i], items[j] = items[j], items[i]
	}
}

func main() {
	uafSeed = envSeed()
	n := 160 + int(uafSeed%41)
	state := uint64(uafSeed)*0x5851f42d4c957f2d + 0x1234567

	recs := make([]*rec, n)
	for i := range recs {
		recs[i] = &rec{id: int64(i), name: fmt.Sprintf("t%d", i), blob: make([]byte, 8+i%6)}
		recs[i].blob[0] = byte(i)
		recs[i].blob[len(recs[i].blob)-1] = byte(i >> 1)
	}
	recSink = recs

	tree := &AVL[int64, rec]{}
	perm := make([]int64, n)
	for i := range perm {
		perm[i] = int64(i)
	}
	shuffle(perm, &state)
	for i, k := range perm {
		old, existed := tree.Insert(k, recs[k])
		ck(!existed && old == nil, fmt.Sprintf("insert %d unexpectedly replaced", k))
		if i%32 == 31 {
			gcStress()
		}
	}
	ck(tree.Len() == n, fmt.Sprintf("len got=%d want=%d", tree.Len(), n))
	ck(tree.Valid(), "AVL invariants violated after inserts")
	maxH := 2
	for (1 << maxH) < n+2 {
		maxH++
	}
	maxH *= 2
	ck(tree.Height() <= maxH, fmt.Sprintf("height got=%d exceeds AVL bound %d", tree.Height(), maxH))

	shown := 0
	prev := int64(-1)
	tree.Walk(func(k int64, v *rec) bool {
		ck(k == prev+1, fmt.Sprintf("in-order key got=%d want=%d", k, prev+1))
		ck(v == recs[k], "in-order payload pointer mismatch")
		if v != nil {
			ck(v.id == k && v.name == fmt.Sprintf("t%d", k), "in-order payload corrupted")
			ck(len(v.blob) == 8+int(k)%6 && v.blob[0] == byte(k), "in-order blob corrupted")
		}
		prev = k
		shown++
		return true
	})
	ck(shown == n, fmt.Sprintf("walk visited %d want %d", shown, n))

	// 重复插入同一 key 应替换并返回旧指针。
	old, existed := tree.Insert(perm[0], recs[(perm[0]+1)%int64(n)])
	ck(existed && old == recs[perm[0]], "duplicate insert did not return old pointer")
	tree.Insert(perm[0], recs[perm[0]])
	ck(tree.Len() == n, "duplicate insert changed size")
	gcStress()
	for i := 0; i < n; i += 17 {
		v, ok := tree.Get(int64(i))
		ck(ok && v == recs[i], fmt.Sprintf("Get(%d) pointer mismatch", i))
	}
	_, ok := tree.Get(int64(n + 5))
	ck(!ok, "Get of absent key returned a value")

	// 删除一半 key（乱序），剩余中序必须等于升序存活集合。
	alive := make([]bool, n)
	for i := range alive {
		alive[i] = true
	}
	deletedCount := 0
	for i, k := range perm {
		if i%2 != 0 {
			continue
		}
		old, ok := tree.Delete(k)
		ck(ok && old == recs[k], fmt.Sprintf("delete %d returned wrong pointer", k))
		if ok {
			alive[k] = false
			deletedCount++
		}
		if deletedCount%32 == 0 {
			gcStress()
		}
	}
	ck(tree.Len() == n-deletedCount, fmt.Sprintf("len after delete got=%d want=%d", tree.Len(), n-deletedCount))
	ck(tree.Valid(), "AVL invariants violated after deletes")
	prev = -1
	shown = 0
	tree.Walk(func(k int64, v *rec) bool {
		for prev+1 < int64(n) && !alive[prev+1] {
			prev++
		}
		ck(k == prev+1, fmt.Sprintf("post-delete in-order key got=%d want=%d", k, prev+1))
		ck(v == recs[k], "post-delete payload pointer mismatch")
		prev = k
		shown++
		return true
	})
	ck(shown == n-deletedCount, "post-delete walk count mismatch")
	for i := 0; i < n; i++ {
		v, ok := tree.Get(int64(i))
		if alive[i] {
			ck(ok && v == recs[i], fmt.Sprintf("alive key %d lost after delete", i))
		} else {
			ck(!ok, fmt.Sprintf("deleted key %d still visible", i))
		}
	}
	gcStress()
	ck(tree.Valid(), "AVL invariants violated after GC")

	// 删除剩余 key，树必须回到空且高度为 0。
	for i := 0; i < n; i++ {
		if !alive[i] {
			continue
		}
		old, ok := tree.Delete(int64(i))
		ck(ok && old == recs[i], "final delete returned wrong pointer")
	}
	ck(tree.Len() == 0 && tree.Height() == 0, "tree not empty after deleting all keys")
	ck(tree.Valid(), "empty tree invalid")
	_, ok = tree.Delete(0)
	ck(!ok, "delete from empty tree succeeded")
	gcStress()

	// 第二次实例化 AVL[string,rec]：字符串键。
	st := &AVL[string, rec]{}
	keys := make([]string, 32)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%02d", i)
	}
	for i := len(keys) - 1; i > 0; i-- {
		j := int(nextRand(&state) % uint64(i+1))
		keys[i], keys[j] = keys[j], keys[i]
	}
	for _, k := range keys {
		idx, _ := strconv.Atoi(k[1:])
		old, existed := st.Insert(k, recs[idx])
		ck(!existed && old == nil, "string insert returned old value")
	}
	ck(st.Len() == 32 && st.Valid(), "string AVL invalid after inserts")
	gcStress()
	want := "k00"
	st.Walk(func(k string, v *rec) bool {
		ck(k == want, fmt.Sprintf("string in-order got=%s want=%s", k, want))
		idx, _ := strconv.Atoi(k[1:])
		ck(v == recs[idx], "string AVL payload pointer mismatch")
		want = fmt.Sprintf("k%02d", idx+1)
		return true
	})
	for i := 0; i < 32; i += 2 {
		cfg := fmt.Sprintf("k%02d", i)
		old, ok := st.Delete(cfg)
		idx, _ := strconv.Atoi(cfg[1:])
		ck(ok && old == recs[idx], "string AVL delete mismatch")
	}
	gcStress()
	ck(st.Len() == 16 && st.Valid(), "string AVL invalid after deletes")

	finish("l01_generic_avl_tree")
}
