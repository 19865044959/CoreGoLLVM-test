---
id: l12_global_struct_gc
dim: L12_init_link
priority: P1
sources:
  - gorm@v1.30.0: callbacks/create_test.go:14 包级 var schemaCache = &sync.Map{} 启动即分配
  - go-redis@v9.8.0: main_test.go:56 包级 clusterScenario 在启动期构造 map/slice
tags: [L12, global-struct, gc, pointers, map, slice, interface, self-reference]
requires: []
runtime: "<10s"
---

# 场景

包级 `struct` 在初始化期构造 32 节点链表、`map[int]*node` 索引、节点切片、指向标量的指针和 `[]interface{}` 盒子，并在 init 中建立自环与反向引用。随后在主程序插入新节点、改写全局根，多轮 GC 后校验指针身份、链表、map 与接口值。

# 期望行为

1. init 后全局 struct 自环成立，label 为 `root/init`，list/map 各 32 项；
2. `head == list[0]`，每个节点的 `next` 指向下一项，尾部为 nil，map 项与 slice 元素为同一指针；
3. 全局标量指针 `*counter=41`；盒子中 struct/node/标量指针类型断言与身份正确；
4. 插入 seed 决定 id 的新节点后：list/map 变 33 项，新节点成为 head 且指向旧 head，标量变 42，label 变 `root/init/mut`；
5. 每次 GC + 垃圾分配后上述身份与内容保持一致，旧节点不受影响；
6. 最后一行 `ALL PASS l12_global_struct_gc`。

# 失败判读

- 指针身份失败（`byID[i] != list[i]`）：全局 struct 内嵌指针被错误复制/重定位；
- GC 后 nil/内容损坏：全局根的 gcdata/gcprog（map/slice/interface）扫描错误；
- 自环或链表遍历异常：初始化期构造的环形引用未被保留；
- 失败行 `seed=` 可用于复现同一插入 id。

# 参数

- `UAF_SEED`：决定插入节点的 id（`100 + seed%8`）与验证顺序；断言与顺序无关，失败输出带种子。

# 运行

```bash
go build -o /tmp/case ./cases/L12_init_link/l12_global_struct_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
