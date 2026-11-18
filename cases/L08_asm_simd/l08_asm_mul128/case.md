---
id: l08_asm_mul128
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L08，密码学/哈希汇编热点）
  - docs/research/klauspost-compress.md:31（汇编与 Go 结果交叉验证）
tags: [L08, asm, amd64, mul, 128-bit]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟大整数/哈希乘法：手写 amd64 汇编用 MULQ 返回 64x64→128 位无符号乘积（hi,lo），用 IMULQ 做有符号低 64 位乘法。

## 期望行为

1. 300 个定种子值中取 120×9 组，`mul128Asm` 的 hi/lo 与 `bits.Mul64` 完全一致；
2. 含 0、1、2^32-1、0xffffffffffffffff 等边界；
3. 有符号乘法含负数、2^62 量级与溢出回绕，结果与 Go 乘法一致；
4. GC 后再次乘法结果不变；
5. 最后一行 `ALL PASS l08_asm_mul128`。

## 失败判读

- hi/lo 颠倒：ABI0 结果槽偏移写错；
- 有符号结果错误：误用 MULQ 或 IMULQ 操作数覆盖了 RDX；
- 仅大数错误：未使用 RDX:RAX 全宽结果；
- 构建失败：非 amd64。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_mul128
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
