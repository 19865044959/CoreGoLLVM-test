---
id: l17_inline_boundary
dim: L17_stackmap
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: A12 内联帧
tags: [L17, inline, noinline, aggregate]
requires: []
runtime: "<3s"
---

# 场景

小函数被内联后，调用点仍是 GC 安全点；对比 `//go:noinline` 版本。另覆盖内联函数的聚合参数（`[2]*node`）、内联分配结果、链式指针透传。

# 期望行为

1. 内联/noinline 叶子函数在 GC 后结果一致；
2. 内联聚合参数的两个指针在 GC 后仍有效；
3. 内联分配出的对象在 GC 后内容正确；
4. 100 轮稳定，最后一行 `ALL PASS l17_inline_boundary`。

# 失败判读

- 内联版本失败而 noinline 通过：内联帧的 stack map 与调用点不一致；
- 聚合参数指针失效：内联后的 aggregate 拆分/depopt 参数缺失；
- 仅 clobberfree 下失败：内联对象被提前回收。

# 参数

无随机路径；是否真正内联由编译器决定，两种实现都应通过。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_inline_boundary/*.go
GODEBUG=clobberfree=1 /tmp/case
```
