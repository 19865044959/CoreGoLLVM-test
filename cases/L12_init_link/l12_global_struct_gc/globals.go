package main

type gnode struct {
	id    int
	label string
	next  *gnode
}

type gstore struct {
	label   string
	byID    map[int]*gnode
	list    []*gnode
	head    *gnode
	self    *gstore
	boxes   []interface{}
	counter *int64
}

var globalCounter int64 = 41

func buildChain(n int) (*gnode, []*gnode) {
	var head, prev *gnode
	list := make([]*gnode, 0, n)
	for i := 0; i < n; i++ {
		cur := &gnode{id: i, label: "n"}
		if prev != nil {
			prev.next = cur
		} else {
			head = cur
		}
		prev = cur
		list = append(list, cur)
	}
	return head, list
}

func newGlobalStore() *gstore {
	head, list := buildChain(32)
	m := make(map[int]*gnode, 32)
	for _, n := range list {
		m[n.id] = n
	}
	return &gstore{
		label:   "root",
		byID:    m,
		list:    list,
		head:    head,
		counter: &globalCounter,
	}
}

// 包级变量初始化：struct/map/slice/interface 中持有一组互相关联的指针。
var globalStore = newGlobalStore()
var globalBoxes = []interface{}{globalStore, globalStore.list[0], &globalCounter}

// init 在全部包级变量就绪后建立自环与反向引用。
func init() {
	globalStore.self = globalStore
	globalStore.boxes = globalBoxes
	globalStore.label += "/init"
}
