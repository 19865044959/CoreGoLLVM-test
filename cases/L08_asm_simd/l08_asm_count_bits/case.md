---
id: l08_asm_count_bits
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/klauspost-compress.md:60（BMI2 汇编路径与通用位运算路径结果必须一致）
tags: [L08, asm, amd64, BSF, popcount, bit-count]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟压缩/哈希库中的“位计数”热路径：逐 64 位字用 `BSFQ` 定位最低置位，再用 `x & (x-1)` 清除该位，循环直到字为 0，累加总置位数。与 `math/bits.OnesCount64` 求和全量对照。该实现不依赖 POPCNT 扩展，只使用基线 amd64 指令。

# 期望行为

1. 长度 0/1/2/3/7/8/9/63/64/65/1000/4097 的混合模式（全 1/全 0/交替/单比特/定种子伪随机）总置位数与 Go 一致；
2. 1000 个全 1 字的总数为 64000，1000 个全 0 字为 0；
3. 从 `&s[1]` 开始、长度 `n-3` 的子切片计数与 Go 一致；
4. 每轮显式 GC + 垃圾分配后源数据与计数结果不变；
5. nil + n=0 返回 0；最后一行 `ALL PASS l08_asm_count_bits`。

# 失败判读

- 计数偶发偏大/死循环：`BSFQ` 后未真正清除最低位（`LEAQ -1(DX)`/`ANDQ` 写错）；
- 空字崩溃：`TESTQ DX,DX` 判空缺失导致 `BSFQ` 输入 0；
- 尾部错误：字步长 8 字节或长度递减写错；
- GC 后错误：Go 侧未保持切片存活；
- 结果与 POPCNT 实现不一致：本用例不依赖 POPCNT，若仅在特定 CPU 失败，检查是否误用了非基线指令。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_count_bits
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
