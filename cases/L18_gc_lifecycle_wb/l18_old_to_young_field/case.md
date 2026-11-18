---
id: l18_old_to_young_field
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: C16 写屏障缺失/错误
tags: [L18, write-barrier, old-gen, interface, map, slice]
requires: []
runtime: "<3s"
---

# 场景

先让对象晋升为老对象，再通过 noinline setter 把新分配对象写入老对象的字段、slice 元素、map 值、interface、数组元素和全局槽，写入后立即 GC。missing write barrier 时，年轻对象会被误回收。

# 期望行为

1. 每次写入后 GC，所有引用仍指向 `name="y"` 且 `v%16==7` 的对象；
2. 字段/slice/map/interface/数组/全局六条路径全部成立；
3. 30 轮稳定，最后一行 `ALL PASS l18_old_to_young_field`。

# 失败判读

- `FAIL: cell.* corrupted`：对应路径的写屏障未生效，年轻对象被回收；
- clobberfree 下 `v` 变成 `0xdeadbeef` 类垃圾值：确定漏写屏障；
- efence 下 SIGSEGV：对象内存已 unmap。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_old_to_young_field/*.go
GODEBUG=clobberfree=1 /tmp/case
```
