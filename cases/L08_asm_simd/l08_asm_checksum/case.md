---
id: l08_asm_checksum
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/fiber.md:55（大请求体读取后的长度/校验和逐项校验）
tags: [L08, asm, amd64, adler32, checksum, mod]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟协议/存储校验热路径：手写 amd64 汇编实现 Adler-32，逐字节更新 `a`（字节和）与 `b`（`a` 的累加和），每步用一次条件减完成 mod 65521，最后拼成 `uint32`。与标准库 `hash/adler32.Checksum` 全量对照，覆盖大缓冲、极值填充、子切片与 GC。

# 期望行为

1. 长度 0/1/15/16/17/255/1024/5552/10000/65537 的 seq/zero/ff/rand 四类数据，asm 校验和与 `adler32.Checksum` 完全一致；
2. 从 `&b[2]` 开始、长度 `n-3` 的子切片校验和与标准库一致；
3. 每轮显式 GC + 垃圾分配后源数据与校验和不变；
4. nil + n=0 返回 1（Adler-32 空输入初值）；
5. 最后一行 `ALL PASS l08_asm_checksum`。

# 失败判读

- 结果整体偏差：`a` 初值不是 1，或结尾 `b<<16|a` 的移位/或运算写错；
- 大缓冲后偏差：条件减只做了一次但累加超过 2*65521（检查每步归约位置）；
- 小缓冲正确、大缓冲错误：`a`/`b` 未持续归约导致溢出或比较分支条件反了；
- 子切片错误：起始指针偏移；
- 返回值为未初始化数据：`MOVL` 未写回 `ret+16(FP)`；
- GC 后错误：Go 侧未保持切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_checksum
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
