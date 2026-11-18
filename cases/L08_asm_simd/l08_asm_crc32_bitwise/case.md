---
id: l08_asm_crc32_bitwise
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/etcd/pkg/crc/crc_test.go:15（自实现 CRC 需与 stdlib crc32 在续算/Reset 上完全一致）
  - docs/research/go-redis.md:28（CRC16 标准向量与二进制 key 校验）
tags: [L08, asm, amd64, crc32, checksum, bitwise, IEEE]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟 WAL/协议帧校验和热路径：汇编实现反射多项式 `0xedb88320` 的位串行 CRC-32（每字节 8 次移位/条件异或），先对初始 crc 取反、结束时再取反，从而与 `hash/crc32.Update(crc32.IEEETable, ...)` 的任意初始值续算语义一致。覆盖标准向量、空输入、分块链式续算与 GC。

# 期望行为

1. 标准向量 `"123456789"` 从 0 起算得到 `0xcbf43926`；
2. 种子 0/1/0xffffffff/0x12345678/0xdeadbeef/0x80000000 下，长度 0/1/2/3/15/16/17/31/32/33/255/256/1024/4096 的结果与 `crc32.Update` 一致；
3. 空输入原样返回初始 crc（取反两次相互抵消）；
4. 1,000,000 个 `'a'` 的结果与标准库一致；
5. 按 1/2/7/16/33/128/509/1024/2376 字节分块，每段以前段结果作为初始 crc，最终值等于整段一次计算的结果；
6. 每轮显式 GC + 垃圾分配后结果不变，输入字节不被修改；
7. 最后一行 `ALL PASS l08_asm_crc32_bitwise`。

# 失败判读

- 所有结果系统性偏移：入口/出口漏了 `NOTL`，或只做了一次取反；
- 每字节只处理 1 位：位循环计数寄存器被覆盖（如复用了 `R8`）；
- 结果与标准库不同但空输入正确：反射多项式写成了非反射形式，或 `XORL` 的立即数被符号扩展影响（仅低 32 位参与，需核对指令编码）；
- 续算结果错误：未把入参 crc 作为 `crc ^ 0xffffffff` 的起点；
- 返回值为 n 或垃圾：ABI0 下 uint32 返回值按 8 字节槽对齐，返回槽偏移误写成 20 而非 24；
- GC 后结果变化：Go 侧未保持切片存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_crc32_bitwise
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
