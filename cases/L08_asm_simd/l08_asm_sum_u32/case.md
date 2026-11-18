---
id: l08_asm_sum_u32
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/research/klauspost-compress.md:48（snappy/s2 汇编边界扫描与逐块处理模型）
tags: [L08, asm, amd64, uint32, sum, unroll]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟校验/统计热路径：手写 amd64 汇编对 `[]uint32` 做 4 路展开求和，使用 64 位累加器避免 32 位回绕。

## 期望行为

1. 长度 0/1/2/3/4/7/8/…/100000 的 asm 求和与 Go 64 位求和一致；
2. 从 `&s[1]` 开始、长度 n-2 的子切片求和等于总和减首尾元素；
3. 100 个 0xffffffff 的累加结果为 `100*(2^32-1)`，不发生 32 位回绕；
4. 每轮 GC + 垃圾分配后源切片内容与 asm 结果不变；
5. nil + n=0 返回 0；最后一行 `ALL PASS l08_asm_sum_u32`。

## 失败判读

- 求和偏差：4 路步长（16 字节）/尾部（4 字节）推进错误；
- 大值结果错误：使用了 32 位累加寄存器；
- 子切片错误：起始指针偏移错误；
- GC 后错误：切片未保持存活。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_sum_u32
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
