---
id: l13_map_grow_delete_gc
dim: L13_runtime_ds
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: map 扩容/删除路径
tags: [L13, map, growth, delete, gc]
requires: []
runtime: "<5s"
---

# 场景

map[int]*node 从 1000 项开始：删除一半、GC、重新插入、再扩容到 3000 项。覆盖 map 扩容（evacuate）、删除、重插路径上的指针值扫描。

# 期望行为

1. 删除偶数键后 len=500，已删键不存在，奇数键值正确；
2. 重插偶数键（新值 `i+100000`）后 len=1000，新旧值都正确；
3. 扩容到 3000 项后 `m[2999].v==2999`；
4. 遍历求 `v%1000` 之和为 1498500；
5. 最后一行 `ALL PASS l13_map_grow_delete_gc`。

# 失败判读

- 值错位/nil：map 扩容搬移时指针未按类型位图处理；
- 已删除键复活：删除路径元数据错误；
- 仅 clobberfree 下失败：map 桶里的指针未被扫描，对象被回收。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L13_runtime_ds/l13_map_grow_delete_gc/*.go
GODEBUG=clobberfree=1 /tmp/case
```
