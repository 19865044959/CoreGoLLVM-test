---
id: l08_asm_crc32_table
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD）
  - research/repos/etcd/pkg/crc/crc_test.go:16（自实现 CRC 与 hash/crc32 在任意初始 crc 上续算一致）
  - research/repos/etcd/pkg/crc/crc_test.go:21（以上一段 Sum32 作为下一段初值）
tags: [L08, asm, amd64, crc32, table-driven, IEEE, chunked]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟 WAL/协议帧校验的查表法 CRC-32 IEEE：每字节执行 `crc = table[(crc^b)&0xff] ^ (crc>>8)`，入口/出口各取反一次，因此支持任意初始 crc 续算；256 项表由 Go 侧 `crc32.MakeTable` 生成并以指针传入汇编。与 `crc32.Update` 对照，是位串行 CRC 用例之外的另一种实现路径。

# 期望行为

1. 标准向量 `"123456789"` 从 0 起算得到 `0xcbf43926`；
2. 种子 0/1/0xffffffff/0x12345678/0xdeadbeef/0x80000000 下，长度 0/1/2/3/15/16/17/31/32/33/255/256/1024/4096 的结果与 `crc32.Update` 一致；
3. 空输入原样返回初始 crc（两次取反抵消）；
4. 1,000,000 个 `'a'` 与 65537 字节全零的结果与标准库一致；
5. 按 1/2/7/16/33/128/509/1024 字节分块，每段以前段结果作为初始 crc，最终值等于整段一次计算；
6. 每轮显式 GC + 垃圾分配后结果不变，输入字节与查表不被修改；
7. 最后一行 `ALL PASS l08_asm_crc32_table`。

# 失败判读

- 所有结果系统性偏移：入口/出口漏了 `NOTL`，或只做一次取反；
- 查表下标错误：`(crc^b)&0xff` 少了掩码，或表索引按字节而非 4 字节缩放；
- 续算结果错误：未把入参 crc 作为 `^crc` 的起点；
- 结果只有部分长度错误：`crc>>8` 用了 64 位寄存器导致高位残留（应使用 32 位 `SHRL`）；
- 返回值为垃圾：ABI0 下 uint32 返回值位于 8 字节对齐槽，偏移误写成 28 而非 32；
- GC 后结果变化：Go 侧未保持输入切片/查表存活。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_crc32_table
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
