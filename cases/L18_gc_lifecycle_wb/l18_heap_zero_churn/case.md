---
id: l18_heap_zero_churn
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: D18 堆对象零初始化/FreeOSMemory
tags: [L18, zero-init, FreeOSMemory, large-object]
requires: []
runtime: "<10s"
---

# 场景

反复分配 `[]*node` 与 `[]kv`，校验新分配全零；填充后 GC；周期性 `debug.FreeOSMemory()` 收缩堆；被 ring 保留的对象必须完好；最后分配 32768 指针的大切片。

# 期望行为

1. 每次新分配的 `[]*node` 全为 nil、`[]kv` 全为零值；
2. GC 后已填充对象抽样正确；
3. FreeOSMemory 后 ring 中的存活对象仍正确；
4. 32768 大切片 GC 后首尾值正确；
5. 最后一行 `ALL PASS l18_heap_zero_churn`。

# 失败判读

- `fresh ... not zeroed`：零初始化被错误删除，残留旧指针；
- `survivor ... corrupted`：堆收缩/回收路径错误；
- clobberfree 下取样值变垃圾：对象被提前回收。

# 参数

无随机路径；循环 15 轮固定。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_heap_zero_churn/*.go
GODEBUG=clobberfree=1 /tmp/case
```
