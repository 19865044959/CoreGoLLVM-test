---
id: l08_asm_struct_fill
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/SOURCES.md:50（go-ethereum 覆盖 L08，汇编热路径）
  - docs/research/sonic.md:165（手写汇编需独立验证 ABI 与栈映射）
tags: [L08, asm, amd64, struct, pointer-field, GC]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟汇编热点填充结果结构体：汇编写入 int64/uint32 字段、string 头（指针+长度）与指针字段，并把入参结构体指针原样返回。验证 ABI0 参数指针映射与跨 GC 的对象存活。

## 期望行为

1. Go 布局断言：B/C/S/P 偏移为 8/12/16/32，sizeof=40；
2. 32 个结构体经汇编填充后字段、字符串内容与指针字段全部正确；
3. 返回指针与入参相同；
4. 显式 GC + 垃圾分配后全部字符串与指针字段仍有效，字段和正确；
5. 对已存活对象反复覆写 64 轮（期间 GC），结果仍正确；
6. 最后一行 `ALL PASS l08_asm_struct_fill`。

## 失败判读

- 字段错位：汇编偏移与 Go 结构体布局不一致；
- 字符串乱码：string 头 ptr/len 槽位写反或长度错误；
- GC 后崩溃/内容损坏：结构体参数未被栈映射标记，或汇编写入指针后对象未被正确扫描；
- 构建失败：非 amd64。

## 参数

仅 linux/amd64；`BUILD_MODE=module`。C 相关标签不适用，本用例为纯 Go+汇编。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_struct_fill
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
