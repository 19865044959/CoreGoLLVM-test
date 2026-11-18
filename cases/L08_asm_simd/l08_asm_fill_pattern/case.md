---
id: l08_asm_fill_pattern
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:35（汇编 expand pattern 的边界写不变量）
tags: [L08, asm, amd64, fill-pattern, memset, sentinel]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟编解码器的“重复模式填充”热路径：手写 amd64 汇编把 64 位模式每 8 字节写入目标，剩余 1..7 字节按小端低字节序逐个补齐。与 Go 参考实现对照；用首尾哨兵检测越界写，覆盖 0..70 全长度、非对齐起始偏移与大缓冲。

# 期望行为

1. n=0..70 × 5 种模式（全 0、递增字节、全 1、混合、高低半交替）的填充内容与 Go 参考实现一致；
2. 4096 字节填充内容一致；
3. 起始偏移 1/3/7/8/9 与长度 0/1/7/8/9/511/512 的组合均正确；
4. 所有场景中目标区间外字节保持哨兵 0xcc（无越界写）；
5. 每轮显式 GC + 垃圾分配后内容与哨兵仍正确；
6. nil + n=0 不访问内存；最后一行 `ALL PASS l08_asm_fill_pattern`。

# 失败判读

- 尾部 1..7 字节错误：未按小端低字节序写出，或 `SHRQ $8` 步进遗漏；
- 模式整体错位：`MOVQ` 循环步长与剩余长度判断写错；
- 哨兵被改写：汇编写超过 n 字节（如尾部循环多走一次）；
- 非对齐偏移错误：起始指针加偏移的时机错误；
- GC 后内容变化：Go 侧未保持缓冲存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_fill_pattern
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
