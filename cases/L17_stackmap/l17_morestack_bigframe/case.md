---
id: l17_morestack_bigframe
dim: L17_stackmap
priority: P1
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: A10 morestack/栈增长
tags: [L17, morestack, stack-copy, recursion]
requires: []
runtime: "<5s"
---

# 场景

2000 层递归，每层有指针局部变量与 512B 指针数组；另有一个 64KB 大栈帧函数。运行中多次强制 GC，栈复制（morestack）发生时所有指针必须被按 stack map 重定位。

# 期望行为

1. `deep(2000)` 返回值等于 9004003（公式见代码），说明每层 buf/local 的指针在 GC 与栈复制后仍正确；
2. `bigFrame` 求和等于预期，64KB 帧扩张时无指针错位；
3. 最后一行为 `ALL PASS l17_morestack_bigframe`。

# 失败判读

- 返回值异常（如 -2^60）：某层指针被破坏，栈复制时未按 map 修正；
- SIGSEGV：栈复制后局部指针指向旧栈；
- 栈溢出：栈增长路径异常（需结合 CoreGoLLVM morestack 实现定位）。

# 参数

无随机路径；深度固定 2000。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_morestack_bigframe/*.go
GODEBUG=clobberfree=1 /tmp/case
```
