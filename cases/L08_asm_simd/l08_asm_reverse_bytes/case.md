---
id: l08_asm_reverse_bytes
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/research/klauspost-compress.md:60（汇编路径与通用路径结果对照，覆盖位/字节重排）
tags: [L08, asm, amd64, reverse, in-place]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟编解码器的原地逆序/字节重排：手写 amd64 汇编用双指针交换反转字节切片，n<=1 直接返回，不解引用。

## 期望行为

1. 长度 0/1/2/3/7/…/1000 的反转结果与 Go 参考实现一致；
2. 连续两次反转恢复原内容（双重反转恒等）；
3. 从偏移 100 开始的 160 字节子切片反转正确，区间外哨兵 0xaa/0xbb 不变；
4. GC 后双重反转与单次反转结果仍正确；
5. nil + n=0/1 不访问内存；最后一行 `ALL PASS l08_asm_reverse_bytes`。

## 失败判读

- 中间元素错误：奇偶长度下双指针相遇条件写错；
- 子切片错误：结束指针 `LEAQ -1(SI)(CX*1)` 计算错误；
- 哨兵被改写：循环多走一步；
- GC 后内容变化：切片被回收或参数未保持存活。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_reverse_bytes
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
