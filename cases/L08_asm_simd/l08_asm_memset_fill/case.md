---
id: l08_asm_memset_fill
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/research/klauspost-compress.md:35（expand/copy 类汇编路径的边界写不变量）
tags: [L08, asm, amd64, memset, fill, sentinel]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟缓冲区清零/填充热路径：手写 amd64 汇编把单字节广播成 8 字节重复字，按 8 字节步进填充，尾部逐字节写入。用首尾哨兵检测越界写。

## 期望行为

1. n=0..70 × 4 种填充字节：仅 `[p, p+n)` 被改写，首尾哨兵保持 0xcc；
2. 填充内容与 `bytes.Repeat` 完全一致；
3. 起始偏移 0/1/7/8/9 与长度 0..4000 的组合均正确，越界哨兵不变；
4. GC 后再次填充 777 字节，内容与哨兵仍正确；
5. nil + n=0 不崩溃；最后一行 `ALL PASS l08_asm_memset_fill`。

## 失败判读

- 尾部字节错误：8 字节广播常数或剩余长度分支错误；
- 哨兵被改写：汇编写超过 n 字节；
- 非对齐起始错误：按 8 字节推进时指针运算错误；
- GC 后崩溃：Go 侧切片未保持存活。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_memset_fill
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
