---
id: l08_asm_hash_loop
dim: L08_asm_simd
priority: P1
sources:
  - klauspost/compress: snappy/s2 汇编哈希/匹配循环（场景表见 docs/research/klauspost-compress.md）
tags: [L08, asm, loop, byte-slice, FNV]
requires: [asm, amd64]
runtime: "<3s"
---

# 场景

手写 amd64 汇编实现 FNV-1a 64 位哈希：循环读字节、异或、乘常数，通过 `p *byte + n int` 访问 Go 字节切片底层数组。与纯 Go 实现逐字节对照，覆盖汇编循环、字节访问、nil/0 边界。

# 期望行为

1. 9 种长度（0/1/2/7/15/64/255/1000/65536）的 asm 哈希与 Go 实现完全相等；
2. 每轮 GC 后源字节与哈希结果不变；
3. `hashAsm(nil,0)` 返回 offset basis `14695981039346656037`，不解引用 nil；
4. 最后一行 `ALL PASS l08_asm_hash_loop`。

# 失败判读

- 哈希不一致：汇编循环/常数/字节加载指令错误；
- 长度 0 崩溃：循环入口未检查 n；
- GC 后不一致：字节切片底层数组被回收（Go 侧缺少 KeepAlive 或汇编参数未被扫描）。

# 参数

仅 linux/amd64；`BUILD_MODE=module`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_hash_loop
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
