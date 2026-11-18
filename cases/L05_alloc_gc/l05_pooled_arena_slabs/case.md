---
id: l05_pooled_arena_slabs
dim: L05_alloc_gc
priority: P1
sources:
  - badger@v4.5.0: y/y_test.go:301 的 TestAllocatorReuse：Allocator Reset 后继续复用底层缓冲
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:71 的 sync.Pool Get/Put 复用与 GC 穿插
tags: [L05, sync.Pool, arena, bump-allocator, reset-reuse, gc]
requires: []
runtime: "<10s"
---

# 场景

模拟内存分配器把 arena 块放进 `sync.Pool` 复用的路径：池中缓存 32KB 块，每次
租用后 bump 游标归零，按 8B 对齐切出 12 条 16–495 字节记录；记录内容与 CRC
快照独立保存，归还后块被后续租约复用。每 40 个租约插入垃圾分配与两次
`runtime.GC()`；最后 4 个租约不归还，跨多轮 GC 校验后再归还并重新租用。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_pooled_arena_slabs`；
2. 每次 Get 时块未被重复租出、游标为 0、块长恰为 32KB，首条记录偏移为 0；
3. 同一租约内记录按 8B 对齐、区间不重叠、不越出块尾；
4. 租用期间每 40 次显式 GC 不改变记录内容，记录视图与块共享底层存储；
5. 全部 2880 份独立快照在归还/复用/GC 后长度与 CRC 不变；
6. 4 个未归还的块跨多轮 GC 后记录仍完整；归还后再次 Get 游标归零；
7. 在 base/clobberfree 等 profile 下结果一致。

## 失败判读

- `slab checked out twice`：池在对象仍被租用时重复借出；
- `first record off=... want=0` / `cursor=`：归还路径未重置 bump 游标；
- `overlaps previous` / `misaligned` / `outside slab`：arena 切分、对齐或边界计算错误；
- `crc changed` / `first byte`：记录底层存储被复用改写或提前回收（UAF）；
- `snapshot ... crc`：快照仍别名池块，池复用覆盖了历史结果；
- `pin=... after GC`：租用中的 arena 块未被正确保活。

## 参数

- `UAF_SEED`：决定每条记录的长度与填充字节（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 240 个租约 × 12 条记录 + 4 个长期租用块。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_pooled_arena_slabs/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
