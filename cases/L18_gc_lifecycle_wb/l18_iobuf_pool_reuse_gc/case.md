---
id: l18_iobuf_pool_reuse_gc
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - pgx@v5.7.4: internal/iobufpool/iobufpool_test.go:71 TestPutGetBufferReuse（Get/Put 循环 + GC 后标记字节验证复用）
tags: [L18, sync.Pool, reuse, size-class, marker, GC]
requires: []
runtime: "<5s"
---

# 场景

按 pgx `iobufpool` 的尺寸类模型（256B 起 2 的幂，最大 32MiB，超过后精确分配）自建 `sync.Pool` 池。用例：

- 多尺寸 Get/Put 循环，穿插 `runtime.GC()`，校验 `len` 精确、`cap` 为正确尺寸类；
- 连续 Get/Put 2048 次，用首字节标记观察缓冲是否被池复用；
- Put 后仍持有外部 `*[]byte` 引用并 GC，标记字节不得被 clobber；
- Put 非 2 的幂 cap 的错误缓冲，不得污染正确尺寸类；
- 32MiB+1 的请求跳过池并精确分配。

# 期望行为

1. 每个尺寸类 Get 返回 `len==请求值`、`cap==对应 2 的幂`；
2. 2048 次连续 Get/Put 中至少观察到一次带上次标记的复用；
3. Put 后的外部引用跨两次 `runtime.GC()` 仍保留 `len/cap/首/中/尾` 标记字节；
4. 错误尺寸 Put 被丢弃，随后 `Get(300)` 仍是 512 cap；
5. GC 后池条目即使被清空，Get 仍返回正确尺寸类；
6. 退出码 0，最后一行 `ALL PASS l18_iobuf_pool_reuse_gc`。

# 失败判读

- `cap=... want=...`：尺寸类计算或 Put 归桶错误；
- `buffer held by external reference corrupted after Put+GC`：GC 误回收仍被外部引用的缓冲，或池 Put/Get 生命周期错误；
- `never reused`：sync.Pool 复用路径失效（若仅该条失败且池实现/调度异常需人工判读）；
- clobberfree/efence 下标记字节变 `0xde...` 或 SIGSEGV：确定 UAF。

# 参数

无随机路径；`UAF_SEED` 不影响结果。固定 24 轮尺寸矩阵 + 2048 次复用探测。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_iobuf_pool_reuse_gc/*.go
GODEBUG=clobberfree=1 /tmp/case
```
