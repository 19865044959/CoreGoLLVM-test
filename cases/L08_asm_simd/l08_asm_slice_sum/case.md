---
id: l08_asm_slice_sum
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD，重点库 klauspost/compress 等）
  - docs/research/klauspost-compress.md:31（s2 汇编实现与纯 Go 实现交叉验证的思路）
tags: [L08, asm, amd64, slice, sum, loop-unroll]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟压缩/哈希库中的“汇编热点对数值切片求和”路径：Go 侧把 `[]int64` 的底层数组指针与长度传给 ABI0 汇编，汇编做 4 路循环展开累加，尾部逐元素处理。覆盖 slice 传参、子切片起始偏移、长切片以及 GC 后底层数组内容不变。

# 期望行为

1. 长度 0/1/3/7/16/63/64/1000/100000 的 asm 求和与 Go 求和完全一致；
2. 从 `&s[1]` 开始、长度 `n-2` 的子切片求和等于总和减去首尾元素；
3. 每轮 `runtime.GC()` + 垃圾分配后，源切片内容与 asm 求和结果不变；
4. 空切片（nil 指针、n=0）返回 0，不解引用；
5. 最后一行 `ALL PASS l08_asm_slice_sum`。

# 失败判读

- 求和错误：汇编 4 路展开的指针步长/尾部循环或 ABI0 参数槽错位；
- 仅子切片错误：起始指针偏移计算错误；
- GC 后错误：Go 侧未保持切片存活或汇编参数栈映射缺失；
- 构建失败：非 amd64 或工具链不支持 Go 汇编（用 `CAPS=asm,amd64` 过滤）。

# 参数

仅 linux/amd64；用例目录含 `BUILD_MODE=module`，按目录构建以应用 `//go:build amd64`。

# 运行

```bash
cd cases/L08_asm_simd/l08_asm_slice_sum
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
