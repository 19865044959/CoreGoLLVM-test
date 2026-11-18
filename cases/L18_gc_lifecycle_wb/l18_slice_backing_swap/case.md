---
id: l18_slice_backing_swap
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - pgx@v5.7.4: pgtype/bytea_test.go:49（行扫描复用同一缓冲，旧结果被后续行覆盖）
  - badger@v4.5.0: y/y_test.go:301（TestAllocatorReuse：Allocator Reset 后 1M 次 Copy/append 重用内存）
tags: [L18, write-barrier, slice-header, backing-array, retired-root, reslice-append, noinline, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 8 轮；TORTURE_ROUNDS 上限 32）"
---

# 场景

全局老 holder 的 `live []*sbNode`（len=cap=48）经双 GC 晋升。每轮 noinline
`sbSwap` 分配全新底层数组并装入 48 个年轻节点，整体替换 slice 头；按
`UAF_SEED` 决定是否把旧底层数组写入 `retired` 全局字段。双 GC 后校验当前代
全部槽位；retired 保留时同时校验上一代对象仍存活且与当前代无共享。

末段对当前 slice 执行 noinline `sbTruncate`（len 24，cap 48）→ GC → 校验；
再用 noinline `sbAppendTail` 在原容量内 append 24 个年轻节点（不重新分配，
写回老底层数组后半段）→ GC → 分段校验新旧混合内容。

与 `old_slice_bulk_rewrite`（原地逐元素覆写）不同，本用例覆盖 slice 头替换、
旧底层数组作为 retired 根、以及截断后原地 append 复用三种边界。

## 期望行为

1. 每轮 live slice 头指向全新底层数组，48 个槽位内容均为当前代；
2. retired 路径下旧底层数组与上一代节点跨 GC 完整，且与 live 不共享对象；
3. 截断后 len=24、cap=48，前 24 槽内容不变；
4. append 后 len=48 且未重新分配，前 24 槽保持旧代、后 24 槽为年轻代；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_slice_backing_swap`。

## 失败判读

- `live ... node corrupted`：新 slice 头写入丢失或底层数组被提前回收；
- `retired ... corrupted`：retired 全局 slice 根漏扫或旧底层数组被复用覆盖；
- `share backing objects`：新旧代发生错误别名；
- `truncate/append ...`：len/cap 维护错误，或原地 append 漏写屏障。

## 参数

- `UAF_SEED`：数据模式与 retired/丢弃路径选择；
- `TORTURE_ROUNDS`：轮数（默认 8，上限 32）。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_slice_backing_swap/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=20 UAF_SEED=6 /tmp/case
```
