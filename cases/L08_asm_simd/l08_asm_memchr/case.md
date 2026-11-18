---
id: l08_asm_memchr
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:48（snappy 汇编边界扫描/overrun 模型）
tags: [L08, asm, amd64, SSE2, memchr, byte-scan]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟 klauspost/snappy 一类库中“汇编字节扫描”热路径：用 SSE2 `PCMPEQB` + `PMOVMSKB` + `BSFL` 一次比较 16 字节，未命中时推进 16 字节，最后不足 16 字节走标量尾部。与 `bytes.IndexByte` 对照。

# 期望行为

1. 长度 0/1/15/16/17/31/32/33/64/65/257 下，目标字节位于首位、中位、末位时 asm 返回的下标与 `bytes.IndexByte` 一致；
2. 目标字节不存在时返回 -1，长度 0 的 nil 输入也返回 -1；
3. 每轮 GC + 垃圾分配后重复扫描，结果不变且源字节不被修改；
4. 最后一行 `ALL PASS l08_asm_memchr`。

# 失败判读

- 块边界附近返回错误：16 字节块循环/尾部循环的指针或剩余长度计算错误；
- 始终 -1：广播字节（PUNPCKLBW/PSHUFL）或 `PCMPEQB` 操作数方向错误；
- 下标为不确定值：命中分支未写回 ABI0 返回槽 `ret+24(FP)`；
- 崩溃：`MOVOU` 越界读取（本实现只整块读，不越过 n）。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_memchr
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
