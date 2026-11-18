---
id: l05_bucket_allocator
dim: L05_alloc_gc
priority: P1
sources:
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:10 的 TestGetCap：256B–32MB 尺寸类分桶
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:38 的 TestPutHandlesWrongSizedBuffers：错误尺寸归还不得污染桶
  - badger@v4.5.0: y/y_test.go:301 的 TestAllocatorReuse：底层缓冲 Reset 后复用
tags: [L05, bucket-allocator, size-class, free-list, slab, slot-reuse, gc]
requires: []
runtime: "<10s"
---

# 场景

自建 size-bucket 分配器：6 档固定尺寸（16/32/64/128/256/512B），每档在 4KB
slab 上切槽，释放槽进入 free-list。每档先分配 96 个槽并填充；释放一半、GC，
再按相同数量重新分配，验证新槽不与仍存活槽重叠、freed 快照未变；最后释放
全部并重新分配，确认 slab 数不增长。每个阶段穿插垃圾分配和两次 `runtime.GC()`。

## 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l05_bucket_allocator`；
2. 每次分配 `len(buf)==尺寸档`，槽偏移按尺寸对齐，同一槽不会被重复分配；
3. 分配后 GC，所有存活槽内容/FNV 与快照一致；
4. 释放一半并 GC 后，保留槽不变，freed 槽的独立快照不变；
5. 重新分配等量槽时，不返回任何仍然 live 的槽；
6. 释放全部后 free-list 覆盖全部槽，重新分配不会新增 slab；
7. 每阶段 GC 不改变存活槽内容；clobberfree 下无串写。

## 失败判读

- `double or invalid alloc` / `allocated while still live`：free-list 管理错误，导致同一槽被两次借出；
- `misaligned`：桶内偏移计算错误；
- `byte mismatch` / `fnv mismatch`：槽被跨档复用或底层 slab 被提前回收；
- `slabs grew after full recycle`：释放未真正回到 free-list，产生泄漏/碎片；
- `free-list len ...`：used 标记与 free-list 长度不一致。

## 参数

- `UAF_SEED`：决定各槽填充字节与快照内容（默认 1，解析失败回退 1），失败汇总行带出种子；
- 无 TORTURE 参数；固定 6 档 × 96 槽。

## 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_bucket_allocator/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
