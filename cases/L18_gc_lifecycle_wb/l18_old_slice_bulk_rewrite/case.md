---
id: l18_old_slice_bulk_rewrite
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - pgx@v5.7.4: pgtype/bytea_test.go:49（行扫描复用同一缓冲，旧结果被后续行覆盖）
tags: [L18, write-barrier, old-slice, bulk-rewrite, copy, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

老 bucket 持有一个已晋升的 64 元素 `[]*item` 底层数组。16 轮中每轮按 `UAF_SEED` 的随机顺序选取 3/4 槽位批量改写：

- 大多数轮由 noinline `rewriteRange` 逐元素写入新对象；
- 每 4 轮用 noinline `copyRewrite` 先在函数内构造年轻指针切片，再逐槽写回老 slice。

改写后 GC，按槽位期望代次校验旧对象/新对象混合状态。最后一轮全量 `copyRewrite`，确保每个槽位都由年轻对象覆盖。

# 期望行为

1. 每轮 GC 后，被改写槽位的 slot/ver/name/24 字节模式与期望代次一致；
2. 未被改写的槽位仍保持上一代内容，不发生串槽；
3. 最后一轮全量改写后 64 个槽位全部为新代次，GC 两轮后仍正确；
4. 失败输出带 `seed=<UAF_SEED>`；
5. 退出码 0，最后一行 `ALL PASS l18_old_slice_bulk_rewrite`。

# 失败判读

- `old slice element N expected ver V corrupted`：老 slice 底层数组写年轻指针漏写屏障，或 copy 路径未触发写屏障；
- 连续槽位内容错位：批量下标计算问题（非 GC），先核对 order；
- clobberfree 下 data 变 0xde、efence 下 SIGSEGV：确认 UAF；
- 失败时用 `UAF_SEED=<seed>` 复现改写顺序。

# 参数

- `UAF_SEED`：每轮批量改写顺序的种子（默认 1），仅影响顺序不影响最终期望；
- 固定参数：64 元素、16 轮、每轮 3/4 槽位。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_slice_bulk_rewrite/main.go
UAF_SEED=7 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
