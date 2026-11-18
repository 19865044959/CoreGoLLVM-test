---
id: l08_asm_bitops
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:60（BMI2/位运算汇编路径与通用路径结果对照）
tags: [L08, asm, amd64, popcount, rotate, SWAR]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟压缩/哈希库中的位运算热点：用 SWAR（不依赖 POPCNT 扩展）统计 64 位 1 的个数，用 `ROLQ` 做循环移位。与 `math/bits.OnesCount64` / `bits.RotateLeft64` 全量对照，覆盖 0、全 1、交替位、2000 个定种子伪随机值以及 k=0..130（含大于 64 的计数）。

# 期望行为

1. 全部 2011 个输入值的 asm popcount 与 `bits.OnesCount64` 一致；
2. k=0..130、前 64 个输入值的 asm 循环移位与 `bits.RotateLeft64` 一致（x86 ROL 自动按 64 取模）；
3. GC 后重复计算一致；
4. 最后一行 `ALL PASS l08_asm_bitops`。

# 失败判读

- popcount 偶发偏差：SWAR 掩码常数或乘法后右移位数错误；
- rotl 偏差：未使用 `ROLQ` 或移位计数寄存器用错；
- k>64 偏差：实现未按 64 取模（x86 硬件会自动取模，纯 Go 对照需一致）；
- 构建失败：非 amd64。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不要求 CPU 支持 POPCNT。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_bitops
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
