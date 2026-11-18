---
id: l08_asm_clz_u64
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/tailscale/util/zstdframe/zstd_test.go:77（用 bits.LeadingZeros64 计算尺寸上界，零值语义关键）
  - docs/research/protobuf-go.md:33（64 位字位运算的边界字/位）
tags: [L08, asm, amd64, clz, BSR, LeadingZeros64]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟变长整数/压缩块头解析中的前导零计数热路径：`BSRQ` 取最高置位下标，前导零数 = 63 - 下标；输入为 0 时 `BSR` 不产生有效结果，必须显式返回 64。与 `math/bits.LeadingZeros64` 对照。

# 期望行为

1. 0、1..8、15/16/17、`1<<31`、`2^32-1`、`1<<32`、`2^63-1`、`1<<63`、全 1 等边界值与 512 个定种子伪随机值的结果与 `bits.LeadingZeros64` 一致；
2. 单个 1 位于第 k 位时返回 `63-k`（k=0..63）；
3. `^uint64(0) >> k` 的最高 k 位为 0，返回 k（k=0..63）；
4. 返回值始终在 0..64 范围内，零值输入返回 64；
5. 每轮显式 GC + 垃圾分配后结果不变；
6. 最后一行 `ALL PASS l08_asm_clz_u64`。

# 失败判读

- 零值输入返回 0：缺少 `x==0` 分支，`BSR` 未定义结果直接参与 `63-index`；
- 非零输入系统性差 1：用了 `63-index` 之外的常数，或下标与计数混淆；
- 只有高位为 1 的输入错误：立即数或 `BSRQ` 操作数宽度用了 32 位变体（`BSRL`）；
- 全部返回 63 或 0：`XORQ $63` 写成了错误的位宽/操作数；
- 固定输入正确但伪随机值错误：`BSRQ` 的源/目标寄存器写反导致读到参数外的值。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_clz_u64
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
