---
id: l08_asm_dot_i16
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/research/sonic.md:165（SSE/AVX2 手写汇编函数需单独验证）
tags: [L08, asm, amd64, SSE2, dot-product]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟音频/量化点积：SSE2 `PMADDWL`（PMADDWD）每轮处理 8 个 int16，得到 4 个 int32 通道，逐个符号扩展到 64 位累加；尾部标量 IMULQ。

## 期望行为

1. 长度 0/1/2/7/8/9/15/16/17/31/32/33/63/64/65/1000 的 asm 点积与 Go int64 点积一致；
2. 从 `&a[1], &b[1]` 开始的子切片点积正确；
3. int16 极值组合（32767/-32768）结果与 Go 一致；
4. 每轮 GC 后源切片与点积结果不变；
5. nil + n=0 返回 0；最后一行 `ALL PASS l08_asm_dot_i16`。

## 失败判读

- 每块结果偏移：4 个 int32 通道的 PSHUFD 轮转次数错误；
- 尾部错误：MOVWQSX 符号扩展缺失；
- 大值错误：int32 通道被溢出累加（应以 64 位累加）；
- GC 后错误：切片未保持存活。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_dot_i16
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
