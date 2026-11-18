---
id: l08_asm_sum_bytes
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:61（xxhash Sum64/Digest 逐字节累计热路径）
tags: [L08, asm, amd64, SSE2, PSADBW, sum-bytes]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟哈希/校验库中“按字节累计”的热路径：SSE2 `PSADBW` 每轮把 16 字节拆成两组 8 字节和，`PADDQ` 累加到两个 64 位通道，尾部逐字节累加，最后水平相加。与纯 Go 64 位累加对照，避免 8 位/16 位累加溢出。

# 期望行为

1. 长度 0/1/2/15/16/17/31/32/33/63/64/65/255/1000/4097 的 asm 求和与 Go 一致；
2. 全 0xff（777 字节）与全 0x00（4096 字节）结果与 Go 一致；
3. 从 `&b[2]` 开始、长度 `n-3` 的子切片求和等于 `b[2:n-1]` 的 Go 求和；
4. 每轮显式 GC + 垃圾分配后源字节与 asm 结果不变；
5. nil + n=0 返回 0；最后一行 `ALL PASS l08_asm_sum_bytes`。

# 失败判读

- 每 16 字节块结果偏移：`PSADBW` 源/目的操作数方向写反，或零向量被上一轮结果覆盖；
- 约为期望值一半：只累加了一个 64 位通道，水平相加遗漏 `PSRLDQ $8`；
- 尾部错误：剩余长度分支或 `MOVBQZX` 步进错误；
- GC 后错误：Go 侧未保持切片存活；
- 崩溃：`MOVOU` 越界读。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_sum_bytes
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
