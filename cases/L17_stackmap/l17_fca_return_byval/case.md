---
id: l17_fca_return_byval
dim: L17_stackmap
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: A4/A5 FCA 聚合参数/返回值
tags: [L17, FCA, aggregate, byval]
requires: []
runtime: "<3s"
---

# 场景

`[2]*node`、`[2][2]*node`、`pair{a,b *node}`、嵌套 struct 作为返回值和按值参数。报告指出 FCA 抽取只支持部分形态，`[N]*T` 等指针字段可能未被 relocate。

# 期望行为

1. 四种聚合返回值在 GC 后字段值正确（1/2、11/22、3/4、6/9/14）；
2. `takeArr2` 返回 101，`takeNested` 返回 72；
3. 10 轮稳定，最后一行 `ALL PASS l17_fca_return_byval`。

# 失败判读

- 聚合里的指针字段变 nil/垃圾：FCA 抽取或 deopt 参数遗漏；
- 仅 clobberfree 下失败：聚合内指针未被扫描，元素被回收；
- 崩溃：返回槽/参数槽被当作非指针或错误偏移。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_fca_return_byval/*.go
GODEBUG=clobberfree=1 /tmp/case
```
