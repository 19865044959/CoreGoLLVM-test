---
id: l18_global_roots
dim: L18_gc_lifecycle_wb
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: B14 全局变量 gcptr/gcprog
tags: [L18, globals, gcprog, map, slice, interface]
requires: []
runtime: "<3s"
---

# 场景

全局 map/slice/interface/单指针持续持有年轻对象，之后改写全局根并扩容，再 GC。验证全局根的扫描位图与写屏障。

# 期望行为

1. 初始 64 个全局元素在 GC 后全部满足 `v%16==7,name=="y"`；
2. 改写 `gm["k0"]`、append `gs`、替换 `gi`、替换 `gn` 后 GC，新值仍有效；
3. 全局 map 扩容到 200 项后首尾项有效；
4. 最后一行 `ALL PASS l18_global_roots`。

# 失败判读

- 全局元素变 nil/垃圾：全局变量 gcdata/ptrdata 标记错误（大类型需 gcprog）；
- 仅 clobberfree 下失败：全局写入未走写屏障或全局根未重扫；
- 崩溃：全局区被扫描到非法指针。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_global_roots/*.go
GODEBUG=clobberfree=1 /tmp/case
```
