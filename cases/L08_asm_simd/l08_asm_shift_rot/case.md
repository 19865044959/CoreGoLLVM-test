---
id: l08_asm_shift_rot
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/research/klauspost-compress.md:60（BMI2 汇编路径与通用路径的位运算结果必须一致）
tags: [L08, asm, amd64, shift, rotate, masking]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟哈希/压缩中的位混合：手写 amd64 汇编实现 SHLQ 逻辑左移、SARQ 算术右移、32 位 RORL 循环右移，验证 x86 对计数取模（64 位移位取低 6 位、32 位旋转取低 5 位）的语义。

## 期望行为

1. k=0/1/7/31/32/63/64/65/127/200 下 SHLQ 与 `x << (k&63)` 一致（含 200 个定种子值）；
2. SARQ 与 Go 有符号右移 `x >> (k&63)` 在 k=0 时也一致；
3. RORL 与 `bits.RotateLeft32(x, -(k&31))` 在全部 k 与 207 个值上一致；
4. GC 后重复移位/旋转结果不变；
5. 最后一行 `ALL PASS l08_asm_shift_rot`。

## 失败判读

- k>=64 偏差：参考实现未按硬件规则取模，或汇编使用了错误的计数寄存器；
- 算术右移错误：误用 SHRQ（逻辑右移）；
- ROR 方向错误：参考值应使用负旋转数；
- 构建失败：非 amd64。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 BMI2。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_shift_rot
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
