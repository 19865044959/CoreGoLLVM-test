---
id: l08_asm_clz_ctz_u64
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/tailscale/util/zstdframe/zstd_test.go:77（用 bits.LeadingZeros64 计算尺寸上界，零值语义关键）
  - research/repos/kitex/pkg/remote/codec/protobuf/protobuf_test.go:85（用 bits.Len64 估算 varint 长度）
tags: [L08, asm, amd64, clz, ctz, BSR, BSF, zero-boundary]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟变长整数/位图解析中同时需要前导零与尾随零的热路径：`BSRQ` 取最高置位下标得到 `clz=63-index`，`BSFQ` 取最低置位下标得到 `ctz`；输入为 0 时两条指令都无有效结果，必须显式返回 64/64。与 `bits.LeadingZeros64`/`bits.TrailingZeros64` 全量对照。

# 期望行为

1. 固定边界值（0、1..8、15/16/17、`1<<31`、`2^32-1`、`1<<32`、`2^63-1`、`1<<63`、全 1、高低半字掩码、交替位模式）的 (clz,ctz) 与标准库一致；
2. 单个 1 位于第 k 位时返回 `(63-k, k)`（k=0..63）；
3. `^0>>k` 的前导零为 k、尾随零为 0；`^0<<k` 的前导零为 0、尾随零为 k；
4. 5000 个定种子伪随机 uint64 的结果与标准库一致；
5. 零输入 `(64,64)`，返回值始终在 0..64；
6. 每轮显式 GC + 垃圾分配后结果不变；
7. 最后一行 `ALL PASS l08_asm_clz_ctz_u64`。

# 失败判读

- 零输入返回 0 或随机值：缺少 `x==0` 分支，`BSR/BSF` 的目标寄存器未初始化；
- 非零输入系统性差 1：`63-index` 的常数或下标与计数混淆；
- ctz 恒为 64：`BSFQ` 的目标写错，或与 clz 共用被覆盖的寄存器；
- 只有高位输入错误：使用了 32 位变体 `BSRL`/`BSFL`，导致位宽截断；
- 两个返回值相同：ABI0 返回槽偏移写错（clz 应在 8、ctz 应在 16）；
- GC 后结果变化：参数按值传递，若出现需检查测试侧变量复用。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_clz_ctz_u64
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
