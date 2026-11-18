---
id: l08_asm_clamp_i64
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/research/sonic.md:165（手写 SSE/AVX 汇编路径需独立验证指令级语义）
tags: [L08, asm, amd64, clamp, cmov]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟量化/限幅路径：手写 amd64 汇编用 CMPQ + CMOVQ 无分支地实现 `clamp(v, lo, hi)`。

## 期望行为

1. 7 组区间 × 513 个值（含 MinInt64/MaxInt64）与 Go 顺序 clamp 一致；
2. 结果始终落在 `[lo, hi]`；lo==hi 时返回该值；
3. 全区间 `[MinInt64, MaxInt64]` 不改变输入；
4. GC 后重复 clamp 一致；
5. 最后一行 `ALL PASS l08_asm_clamp_i64`。

## 失败判读

- 边界值错误：CMOVQ 条件码（LT/GT）或操作数方向写反；
- 下限未生效：只实现了上界 clamp；
- 构建失败：非 amd64。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_clamp_i64
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
