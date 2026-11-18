---
id: l09_cgo_union_bitfield
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo）
  - docs/SOURCES.md:61（klauspost/compress 的汇编/紧凑位布局场景）
tags: [L09, cgo, union, bitfield, layout, little-endian]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库中的紧凑数据结构：`union word_t {uint32 u; uint8 b[4]; float f;}` 与位域结构 `flags_t {a:3,b:5,c:8,d:16}`。cgo 不能直接访问 union/bit-field 成员，本用例通过 C 辅助函数读写，验证 union 别名、float 位模式、位域打包布局与跨 GC 稳定性。

# 期望行为

1. `sizeof(word_t)==4`、`sizeof(flags_t)==4`，且 Go `unsafe.Sizeof` 与 C 一致；
2. `u=0x11223344` 时 `b[0..3]` 为 `0x44,0x33,0x22,0x11`（linux/amd64 小端）；
3. 写 `b[2]=0xAA` 后 `u==0x11AA3344`；写 `float(1.5)` 后 `u==math.Float32bits(1.5)`；
4. 位域 `(5,17,200,40000)` 读回一致，打包值为 `5 | 17<<3 | 200<<8 | 40000<<16`；
5. GC 后 union/位域内容不变；
6. 最后一行 `ALL PASS l09_cgo_union_bitfield`。

# 失败判读

- union 字节序不符：目标不是小端或 C 编译器布局不同（本用例限定 linux/amd64）；
- 位域 packed 不符：编译器位域分配顺序与预期不同（gcc/clang x86-64 从低位分配）；
- 字段读写失败：C 辅助函数签名/类型转换错误；
- cgo 直接访问 union/bit-field 构建失败：应改用 C 辅助函数（本用例即此模式）。

# 参数

需要 `CGO_ENABLED=1` 与 gcc/clang；`BUILD_MODE=module`。布局断言以 linux/amd64 小端 + gcc/clang 位域布局为前提。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_union_bitfield
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
