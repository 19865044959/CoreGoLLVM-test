---
id: l18_pool_liveness
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: C16/D18 写屏障与对象生命周期
tags: [L18, sync.Pool, external-reference, gc]
requires: []
runtime: "<5s"
---

# 场景

对象放入 sync.Pool 后，外部仍持有引用（`keep` 切片）。GC 可能清空池，但外部引用指向的对象必须存活，且复用后内部不变量不被破坏。

# 期望行为

1. 每轮 GC 后 `keep` 中对象的指针字段与缓冲内容一致；
2. 从池中取出的 `New` 对象字段合法；
3. 最后一行 `ALL PASS l18_pool_liveness`。

# 失败判读

- `external reference to pooled object corrupted`：池化对象存活判定/写屏障错误；
- `pooled buffer content corrupted`：缓冲底层数组被回收或复用越界；
- 只 clobberfree 下失败：UAF 已发生。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_pool_liveness/*.go
GODEBUG=clobberfree=1 /tmp/case
```
