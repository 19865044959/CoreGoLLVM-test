---
id: l08_asm_first_nonzero
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/research/klauspost-compress.md:48（snappy 汇编边界扫描模型）
tags: [L08, asm, amd64, SSE2, scan]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟解码器查找首个有效字节：SSE2 每次 16 字节与全零向量比较，`PMOVMSKB` 取掩码后取反，`BSFL` 定位块内首个非零字节，尾部标量扫描；未找到返回 -1。

## 期望行为

1. 长度 0/1/2/15/16/17/31/32/33/63/64/65/255/256/4096 下全零返回 -1；
2. 单个非零位于首/中/末以及稀疏非零模式的结果与 Go 参考实现一致；
3. 从 `&base[64]` 开始的子切片偏移正确，后半段全零返回 -1；
4. GC 后对 1000 字节缓冲的查找结果不变；
5. 最后一行 `ALL PASS l08_asm_first_nonzero`。

## 失败判读

- 块内下标错误：BSFL 前未对掩码取反，或未加块偏移；
- 16 字节边界错误：块循环剩余长度/尾部入口条件错误；
- 始终 -1：广播零向量被上一轮 `PCMPEQB` 覆盖（应保留独立零寄存器）；
- 越界读：未按整块 16 字节推进。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_first_nonzero
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
