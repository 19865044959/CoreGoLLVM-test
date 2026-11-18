---
id: l05_append_growth_gc
dim: L05_alloc_gc
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: 大 makeslice / append 扩容
tags: [L05, append, slice-growth, copy, gc]
requires: []
runtime: "<5s"
---

# 场景

2000 个指针元素经多次 append 扩容（4→…），扩容后立即 GC；随后继续 append、取子切片、copy。检查扩容/复制路径上的指针元素没有丢失或错位。

# 期望行为

1. `build(2000)` 返回后 GC，所有元素 `v==index,name=="app"`，求和为 1999000；
2. 继续 append 200 个元素并 GC，新元素值正确、长度 2200；
3. `s[500:1500]` 与 `copy` 结果在 GC 后正确；
4. 最后一行 `ALL PASS l05_append_growth_gc`。

# 失败判读

- 元素为 nil/值错位：growslice/typedmemmove 路径上的指针未被正确扫描或复制；
- 仅 clobberfree 下失败：扩容时旧数组被提前回收；
- 崩溃：切片底层数组越界或指针损坏。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L05_alloc_gc/l05_append_growth_gc/*.go
GODEBUG=clobberfree=1 /tmp/case
```
