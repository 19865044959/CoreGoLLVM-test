---
id: l08_asm_abi0_amd64
dim: L08_asm_simd
priority: P1
sources:
  - klauspost/compress: 汇编热点（场景表见 docs/research/klauspost-compress.md）
  - golang.org/x/sys: ABI0 汇编约定（Go 汇编文档）
tags: [L08, asm, ABI0, amd64]
requires: [asm, amd64]
runtime: "<3s"
---

# 场景

手写 amd64 Go 汇编（Plan 9 语法，ABI0）：整数加法、指针原样返回、读取 Go 结构体首字段。汇编函数通过 `TEXT ·name(SB)` 实现，Go 侧只有声明。覆盖汇编器、ABI0 wrapper、汇编函数参数指针映射。

# 期望行为

1. `addAsm(i,2)==i+2`、`addAsm(-i,i)==0`（200 轮）；
2. `keepPtr` 返回的指针与入参相同，GC 后对象内容完好；
3. `readV` 循环读到 node.v=42，GC 后仍正确；
4. 最后一行 `ALL PASS l08_asm_abi0_amd64`。

# 失败判读

- 汇编结果错误：汇编器/ABI0 调用约定问题；
- 指针透传后内容损坏：汇编函数参数指针映射缺失，GC 未扫描到入参；
- 构建失败：当前架构不是 amd64，或工具链不支持 Go 汇编（用 `CAPS=asm,amd64` 过滤）。

# 参数

仅 linux/amd64；`BUILD_MODE=module` 走目录构建以应用 `//go:build amd64`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_abi0_amd64
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
