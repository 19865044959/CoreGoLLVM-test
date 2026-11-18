---
id: l08_asm_bitrev_u64
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/klauspost-compress/zstd/zstd_test.go:45（逐位构造/翻转的位序列边界对照）
  - docs/research/protobuf-go.md:33（位图以 64 位字做位运算，位序正确性是核心不变量）
tags: [L08, asm, amd64, bit-reverse, SWAR, bswap, Reverse64]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟压缩/编解码器中“位序翻转”的 SWAR 热路径：先在每个字节内部交换相邻 1 位、2 位、4 位完成字节内位反转，再用 `BSWAPQ` 反转 8 个字节的顺序，得到完整的 64 位位反转。与 `math/bits.Reverse64` 全量对照。

# 期望行为

1. 固定边界值（0、1、2、`1<<63`、全 1、0x0102...0708、0xaaaa...、0x5555...、0xf0f0...、0x0f0f...）与 300 个定种子伪随机值的反转结果与 `bits.Reverse64` 一致；
2. 对合性：连续两次反转恢复原值；
3. 单个 1 位于第 k 位时结果恰为第 63-k 位（k=0..63）；
4. `0x0102030405060708` 反转为 `0x10e060a020c04080`；
5. 逐字节位反转后再反转字节序的结果与整体 64 位反转一致；
6. 每轮显式 GC + 垃圾分配后结果不变；
7. 最后一行 `ALL PASS l08_asm_bitrev_u64`。

# 失败判读

- 只有字节序变化、字节内位序未变：1/2/4 位 SWAP 段的掩码或移位写错；
- 字节内位序正确但整体顺序未变：漏掉 `BSWAPQ`；
- 结果只有低 32 位正确：立即数掩码被截断，或误用了 32 位移位/逻辑指令；
- 部分输入错误：SWAR 中间步骤复用了源寄存器，导致下一步读到被覆盖的值；
- GC 后结果变化：参数按值传递，通常不会变化；若出现需检查 Go 侧是否在 GC 后复用了错误的变量。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_bitrev_u64
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
