---
id: l08_asm_count_zero
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:110（L08 汇编覆盖点与结果一致性要求）
tags: [L08, asm, amd64, SSE2, popcount, zero-bytes]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟编解码器对缓冲区“零字节比例/零填充”的快速统计：SSE2 `PCMPEQB` 与全零向量比较，`PMOVMSKB` 取 16 位掩码，再用 `x & (x-1)` 循环清最低位统计个数；不足 16 字节走标量尾部。与 `bytes.Count(b, []byte{0})` 对照。

# 期望行为

1. 全零、全非零、两种稀疏零模式下，长度 0/1/15/16/17/32/64/255/4096 的计数与 Go 一致；
2. 从 `b[2]` 开始、长度 `len-3` 的子切片计数与 Go 一致；
3. 每轮 GC 后重复统计结果不变，源字节不变；
4. 空输入（nil，n=0）返回 0；
5. 最后一行 `ALL PASS l08_asm_count_zero`。

# 失败判读

- 16 字节块内计数偏小：掩码 popcount 循环写错（未清最低位/未累加）；
- 尾部错误：标量尾部 `CMPB $0,(SI)` 或步进错误；
- GC 后结果变化：Go 侧未保持切片存活；
- 崩溃：`MOVOU` 越界读。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_count_zero
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
