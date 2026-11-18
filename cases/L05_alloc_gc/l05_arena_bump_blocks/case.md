---
id: l05_arena_bump_blocks
dim: L05_alloc_gc
priority: P1
sources:
  - badger@v4.5.0: skl/skl_test.go:162 的 TestConcurrentBasicBigValues：120MB skiplist arena 上的大对象分配
  - badger@v4.5.0: y/y_test.go:301 的 TestAllocatorReuse：Allocator Reset 后继续复用底层缓冲
tags: [L05, arena, bump-allocator, slab, reset-reuse, alignment, gc]
requires: []
runtime: "<10s"
---

# 场景

自建块式 bump arena：从 64KB 块中按 16B 对齐切分 24–503 字节的记录，超出
16KB 的请求直接独立分配。共 4 代，每代 reset 后重新填充 360 条记录并保留
逐字节快照；每代做垃圾分配和两次 `runtime.GC()`。用例校验块复用、记录不重叠、
对齐、快照独立，以及 arena 外的 pinned 大分配不被 reset/GC 破坏。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_arena_bump_blocks`；
2. 每代每条记录 `len` 精确、loc 16B 对齐、同一代内区间不重叠；
3. 首代至少使用 2 个块；后续代 reset 后块数不增长，首块底层数组身份不变；
4. 后代第一条记录从 loc 0 开始，证明 bump 游标回卷而不是重新分配；
5. 每代 GC 后记录的视图内容、FNV 与快照完全一致；
6. 独立分配（48KB）不进入块表，跨代 reset 与 GC 后内容不变；
7. 被 pin 的记录快照与直接分配在 reset/GC 后仍正确。

## 失败判读

- `overlap` / `unaligned`：bump 游标或对齐计算错误；
- `block count ... grew` / `first block ... changed`：reset 未复用块，或块表被意外替换；
- `corrupted after GC`：arena 持有的块被提前回收，或快照/视图别名被复用改写；
- `pinned direct allocation changed`：大分配被误当成块内内存回收。

## 参数

- `UAF_SEED`：决定记录尺寸与字节填充模式（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 4 代 × 360 条记录。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_arena_bump_blocks/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
