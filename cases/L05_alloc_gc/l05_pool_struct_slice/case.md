---
id: l05_pool_struct_slice
dim: L05_alloc_gc
priority: P1
sources:
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:71 的 sync.Pool Get/Put 复用与 GC 穿插
tags: [L05, sync.Pool, struct-with-slice, spans, pointer-fields, gc]
requires: []
runtime: "<10s"
---

# 场景

`sync.Pool` 存放含两个 slice 的结构体 `page`（`buf []byte` 与 `spans []span`，`span` 内含 `*tag`）。500 轮中每轮从池取出、Reset 后写入 8–24 个 span（记录 `buf` 内偏移与独立分配的 `*tag`），持有期间显式 GC 并逐 span 校验内容/指针；归还后隔批 GC（可能清空池），再做 40 次跨 GC 借还，确认新旧对象的 len 归零、owner 非 nil 且复用内容正确。

# 期望行为

1. 每轮借出的 page 满足 `owner != nil`、`len(spans)==0`，Reset 后 span 数与 owner 正确；
2. 每轮 GC 后全部 span 的 `off/n/*tag`、tag 内容与 `buf` 字节逐一匹配，span 区间连续覆盖 `buf`；
3. 归还后再次借出（旧对象或 `New` 对象）长度均归零、owner 非 nil；
4. 跨多次 GC 的 40 次借还中，写入的 3 字节与 span 指针保持正确；
5. 最后一行输出 `ALL PASS l05_pool_struct_slice`。

# 失败判读

- `pooled page state` / `page not reset`：Put/Get 契约失效；
- `tag corrupted` / `nil tag`：结构体内指针字段在 GC 中丢失；
- `span ... range/byte mismatch`：slice 底层数组被复用或 span 偏移错乱；
- 仅 clobberfree/efence 下失败：池对象底层数组被提前回收（UAF）。

# 参数

- `UAF_SEED`：控制每页 span 数与 span 字节模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数，固定 500 轮 + 40 次回收后复用。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_pool_struct_slice/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
