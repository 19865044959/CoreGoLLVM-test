---
id: l18_value_delete_rewrite_gc
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - badger@v4.5.0: value_test.go:207 TestValueGC（32KB 值批量写、删除 45 个、rewrite 后校验未删值）
tags: [L18, write-barrier, large-value, delete, rewrite, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 badger `TestValueGC` 的值日志生命周期：64 个 key 各写入 32KB 带确定性模式的大值，GC 晋升为老对象；按 `UAF_SEED` 随机删除 35–50 个 key；随后对所有未删除 key 全量重写两版（2、3），每次 `storeSet`（noinline）返回后立即 `runtime.GC()`。新值只被老 `store.entries` 引用，缺失写屏障时会被回收。

# 期望行为

1. 初始 64 个值在两次 GC 后逐字节正确；
2. 删除的 key 置 nil，未删除 key 保持第 1 版内容；
3. 两轮全量重写后，每个未删除 key 的版本、key、32KB 模式全部正确，删除 key 始终为 nil；
4. 所有失败输出带 `seed=<UAF_SEED>`，同一种子可复现删除集合；
5. 退出码 0，最后一行 `ALL PASS l18_value_delete_rewrite_gc`。

# 失败判读

- `new value ... collected after write barrier GC`：老 slice 写年轻指针时漏写屏障；
- `live key ... corrupted`：对应对象被提前回收或重写错位；
- `deleted key ... resurrected`：删除路径未清引用/被错误恢复；
- clobberfree 下模式字节变 `0xde...`、efence 下 SIGSEGV：确定 UAF；
- 失败时记录 seed，可用 `UAF_SEED=<seed>` 复现。

# 参数

- `UAF_SEED`：删除集合种子（默认 1；runner 每次注入不同值）；仅影响删除哪些 key，不影响输出文本。
- 固定参数：64 key × 32KB，删除 35–50 个，重写版本 2/3。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_value_delete_rewrite_gc/*.go
UAF_SEED=7 GODEBUG=clobberfree=1 /tmp/case
```
