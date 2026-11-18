---
id: l08_asm_strcmp_lex
dim: L08_asm_simd
priority: P1
sources:
  - docs/DIMENSIONS.md:16（L08 汇编与 SIMD：手写 .s、ABI0、SIMD）
  - docs/research/klauspost-compress.md:31（汇编路径与纯 Go 实现交叉验证，结果必须逐项一致）
tags: [L08, asm, amd64, strcmp, byte-compare]
requires: [asm, amd64]
runtime: "<5s"
---

# 场景

模拟压缩/索引库中的字典序比较热路径：手写 amd64 汇编按无符号字节比较两个字节串（strcmp 语义的有界版本），返回 -1/0/1。汇编循环通过 ABI0 参数槽读取两个指针与长度。

## 期望行为

1. 空输入（nil/0）与相同串返回 0；公共前缀结束后长度较短者返回 -1；
2. 长度 1..1024 的定种子串在首位/中位/末位翻转一字节后，asm 结果与 Go 参考实现一致且非 0；
3. 从非零偏移的子切片比较结果与 Go 一致；
4. 每轮 `runtime.GC()` + 垃圾分配后重复比较，源字节不被修改；
5. 最后一行输出 `ALL PASS l08_asm_strcmp_lex`，失败退出码为 1。

## 失败判读

- 结果符号错误：`CMPQ`/`JLT`/`JGT` 的分支方向或返回槽写入错误；
- 前缀长度判断错误：公共前缀结束后未按长度差返回；
- GC 后结果变化或崩溃：Go 侧切片未保持存活，或 ABI0 参数指针映射缺失。

## 参数

仅 linux/amd64；`BUILD_MODE=module`，通过 `//go:build amd64` 限定。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=asm,amd64` 可过滤本用例。

## 运行

```bash
cd cases/L08_asm_simd/l08_asm_strcmp_lex
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
