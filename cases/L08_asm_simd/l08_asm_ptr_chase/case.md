---
id: l08_asm_ptr_chase
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/sonic.md:35（AST pair/node 链表在块边界处的重接与遍历）
tags: [L08, asm, amd64, linked-list, pointer-chase, GC]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟 AST/索引结构中“汇编指针追逐”热路径：节点为 `{v int64; next *node; name string}`，汇编从链头开始沿偏移 8 的 `next` 字段逐节点推进，累加偏移 0 的 `v`，遇到 nil 结束。Go 侧按固定置换串链（不按分配顺序），并跨 GC 校验节点内容（含 string 字段）、链结构与求和。

# 期望行为

1. Go 布局断言：v/next/name 偏移为 0/8/16，sizeof=32；
2. nil 链返回 0；
3. 长度 0/1/2/7/64/512 的链，asm 求和与 Go 遍历一致；
4. 长度 1000 的链重复 20 轮，每轮显式 GC + 垃圾分配后 asm 求和仍正确；
5. GC 后所有节点的 v/name 完好，链上节点数等于构造数（无断链/错链/提前回收）；
6. 最后一行 `ALL PASS l08_asm_ptr_chase`。

# 失败判读

- 求和偏差：`next` 偏移（8）或 `v` 偏移（0）与 Go 布局不一致；
- 少一个节点：尾节点 next 非 nil 或循环终止条件写错；
- GC 后崩溃/名字乱码：链头未被保持存活，或汇编参数指针映射缺失；
- 链上节点数不符：构造置换逻辑错误（与本用例的 asm 无关）；
- 构建失败：非 amd64（`CAPS=asm,amd64` 过滤）。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_ptr_chase
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
