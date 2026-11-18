---
id: l17_addrtaken_local_array
dim: L17_stackmap
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: A3 地址被取的局部数组
tags: [L17, array, address-taken, zero-point]
requires: []
runtime: "<3s"
---

# 场景

局部数组 / 结构体字段数组取地址后传入 noinline 函数，函数内触发 GC；以及循环体内每轮声明的局部数组。覆盖 addr-taken 栈对象与 zero point 调整。

# 期望行为

1. `fill(&a)` 返回 0..31 之和 496，GC 后数组元素仍正确；
2. 结构体字段数组在 GC 后 `arr[7].v==777`；
3. 循环内局部数组每轮 GC 后值不变；
4. 最后一行 `ALL PASS l17_addrtaken_local_array`。

# 失败判读

- 数组元素被清零/变成 nil：zero point 被错误移动或漏扫栈数组；
- 只在 clobberfree 下失败：数组所在栈槽未被标记为指针区；
- 崩溃：扫描到非法指针。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_addrtaken_local_array/*.go
GODEBUG=clobberfree=1 /tmp/case
```
