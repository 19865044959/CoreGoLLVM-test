---
id: l08_asm_bitrev_bytes
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/nats-server/server/filestore_test.go:1261（用 bits.Reverse8 翻转存储字节，模拟位序损坏）
  - docs/research/protobuf-go.md:33（位图以 64 位字做位运算，位序正确性是核心不变量）
tags: [L08, asm, amd64, bit-reverse, Reverse8, table-lookup, in-place]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟纠错/位序变换热路径：对字节缓冲原地逐字节做 8 位反转，汇编用 Go 侧传入的 256 项查表把 `b` 替换为 `Reverse8(b)`。与 `math/bits.Reverse8` 对照，覆盖全部 256 个字节值、块边界、对合性与 GC。

# 期望行为

1. 长度 0/1/2/3/7/8/15/16/17/31/32/33/63/64/65/1024 的输入逐字节结果与 `bits.Reverse8` 一致；
2. 单缓冲覆盖 0x00..0xff 全部 256 个值，结果完全正确；
3. 首尾哨兵字节不被修改，仅 `[p, p+n)` 被改写；nil/空输入不访问内存；
4. 连续两次反转恢复原文（对合性），且哨兵保持原值；
5. 子切片 `b[2:n-1]` 反转正确；
6. 每轮显式 GC + 垃圾分配后结果不变，256 项查表不被修改；
7. 最后一行 `ALL PASS l08_asm_bitrev_bytes`。

# 失败判读

- 结果像“字节序反转”而非“位序反转”：查表生成时误用了字节交换/整体反转；
- 只有部分值错误：查表下标未做 0..255 掩码，或表长不足 256；
- 哨兵被改写：循环多处理一个字节或指针步进错误；
- 第二次反转未恢复：原地写入后源指针未推进，反复读同一字节；
- GC 后结果变化：Go 侧未保持缓冲/查表存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_bitrev_bytes
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
