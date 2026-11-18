package main

// mutateStore 在包级全局结构上插入新节点，验证初始化后写入的指针跨 GC 有效。
func mutateStore(id int) {
	old := globalStore.head
	n := &gnode{id: id, label: "m", next: old}
	globalStore.head = n
	globalStore.list = append(globalStore.list, n)
	globalStore.byID[id] = n
	*globalStore.counter = 42
	globalStore.label += "/mut"
	if len(globalBoxes) == 3 {
		globalBoxes[1] = n
	}
}
