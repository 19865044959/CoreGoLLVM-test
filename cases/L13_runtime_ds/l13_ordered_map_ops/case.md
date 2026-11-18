---
id: l13_ordered_map_ops
dim: L13_runtime_ds
priority: P2
sources:
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:359 TestIntervalTreeRandom 随机插入/删除/查询后按序 Visit
  - etcd@v3.5.0: pkg/adt/interval_tree_test.go:406 TestIntervalTreeSortedVisit 中序访问必须有序
  - badger@v4.5.0: table/merge_iterator_test.go:162 TestMergeMore 多路有序迭代器归并（源文档 4 路归并场景）
tags: [L13, ordered-map, binary-search, sorted-keys, insert-delete, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 3 轮；TORTURE_ROUNDS 可放大）"
---

# 场景

用“有序 key 切片 + `map[string]*omRec` 索引”实现有序 map，key 为 1024B 公共前缀 + 组号 + 5 位 id：

1. 按 `UAF_SEED` 乱序插入 300 个 key，校验 keys 严格递增且与独立 `sort.Strings` 结果一致；
2. 每轮 3000 次随机 put/update/delete/get/前缀计数，与 `map[int]int` 模型逐操作对照；每 400 步校验 key 顺序、长度、全模型值和 rec 的 pad，并显式 GC 后再校验；
3. 前缀计数：对 id%17 的组做二分定位 + 前缀扫描，结果与模型一致；
4. 删除前半（从头部消费）后剩余顺序不变；再从尾部逐个删除至空，空结构可重新插入并查询；
5. `TORTURE_ROUNDS` 控制外层轮数，默认 3 轮。

# 期望行为

1. 初始与每次插入/删除后 keys 严格递增、无空槽、与 map 长度一致；
2. 3000 次随机操作后模型全部命中，删除/更新返回值正确；
3. 前缀计数与模型一致，二分插入点正确；
4. 头部/尾部批量删除后剩余项顺序与内容正确，GC 后仍有效；
5. 清空后再次插入可用；
6. 最后一行 `ALL PASS l13_ordered_map_ops`。

# 失败判读

- `key order broken`：插入位置计算或 copy 搬移错误；
- `model mismatch`：更新/删除未同步 keys 与 map；
- `prefix count`：二分定位或前缀边界错误；
- `remaining rec bad`：切片搬移后指针/内容损坏；
- 超时：随机操作规模异常或二分死循环。

# 参数

- `UAF_SEED`：插入顺序与随机操作种子，失败信息附 seed；
- `TORTURE_ROUNDS`：轮数，默认 3，上限 20。

# 运行

```bash
go build -o /tmp/l13_ordered_map_ops ./cases/L13_runtime_ds/l13_ordered_map_ops/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/l13_ordered_map_ops
UAF_SEED=4 TORTURE_ROUNDS=8 /tmp/l13_ordered_map_ops
```
