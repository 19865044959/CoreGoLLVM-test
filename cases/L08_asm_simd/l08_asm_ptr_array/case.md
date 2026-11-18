---
id: l08_asm_ptr_array
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD，重点库含 sonic/fasthttp）
  - docs/SOURCES.md:28（sonic 覆盖 L08 等维度，指针对/结构体热路径）
tags: [L08, asm, amd64, pointer-array, struct-field, GC]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟高性能库中“汇编遍历对象指针数组、读取结构体字段”的路径：Go 侧构造 `[]*node`（含 nil 槽位），汇编按 8 字节步长加载指针、判空后解引用读取首字段 `v`。用于验证 ABI0 参数指针映射、汇编内不保存/不破坏指针，以及跨 GC 的节点（含 string 字段）存活。

# 期望行为

1. 64 个槽位（每第 7 个为 nil）的 asm 求和与 Go 求和一致，100 轮均成立；
2. 从 `&arr[3]` 开始的子数组求和一致；
3. 每轮 GC + 垃圾分配后，所有槽位指针、`v`、`name` 字段完好，asm 求和不变；
4. 全 nil 数组与 (nil,0) 返回 0；
5. 最后一行 `ALL PASS l08_asm_ptr_array`。

# 失败判读

- 求和错误：汇编步长/解引用偏移或 nil 判空分支错误；
- GC 后节点内容损坏/崩溃：Go 指针未被正确保持，或汇编参数栈映射缺失；
- 仅子数组错误：起始地址偏移计算错误；
- 构建失败：非 amd64（`CAPS=asm,amd64` 过滤）。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_ptr_array
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
