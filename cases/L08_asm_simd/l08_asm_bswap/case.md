---
id: l08_asm_bswap
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - docs/research/pgx.md:21（二进制协议读写需校验字节序与总量）
tags: [L08, asm, amd64, bswap, endian, ReverseBytes]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟协议编解码中的“字节序转换”热路径：手写 amd64 汇编用 `BSWAPQ` 翻转 64 位、`BSWAPL` 翻转 32 位、`ROLW $8` 翻转 16 位，并对 `[]uint64` 原地逐字交换。与 `math/bits.ReverseBytes64/32/16` 全量对照，覆盖边界值与定种子伪随机值。

为避免 ABI0 窄参数/窄返回值槽位对齐差异干扰被测语义，Go 侧把 32/16 位值统一以 `uint64` 传入/接收，汇编只操作低 32/16 位。

# 期望行为

1. 308 个输入（含 0、1、2^63、全 1、0x0102030405060708）的 64/32/16 位交换结果与 `bits.ReverseBytes*` 一致；
2. 连续两次交换恢复原值（对合性）；
3. `bswapBufAsm` 对长度 0/1/2/7/64/257 的切片原地交换结果与 Go 一致，二次交换恢复原内容；
4. 从 `&s[1]` 开始、长度 `n-2` 的子切片交换正确，区间外元素不变；
5. 每轮显式 GC + 垃圾分配后结果不变；
6. nil + n=0 不访问内存；最后一行 `ALL PASS l08_asm_bswap`。

# 失败判读

- 64 位结果半翻：误用 `BSWAPL` 或结果槽 8 字节只写 4 字节；
- 32 位结果高半污染：`MOVL`/`BSWAPL` 后写回时未清高位或返回槽偏移错误；
- 16 位结果错误：`ROLW` 计数不是 8，或误用 `BSWAPW`（不存在）；
- 切片交换后长度边界元素损坏：循环步长/递减写错；
- GC 后结果变化：Go 侧未保持切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_bswap
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
