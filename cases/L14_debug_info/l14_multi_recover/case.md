---
id: l14_multi_recover
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_multi_recover/main.go:72 两个 defer 依次 recover
  - cases/L14_debug_info/l14_multi_recover/main.go:89 外层 caller 再 recover
  - cases/L14_debug_info/l14_multi_recover/main.go:63 panic 叶子
  - docs/DIMENSIONS.md:22 L14 调试信息（栈展开）
tags: [L14, multi-recover, defer, panic, debug.Stack, closure-symbol, gc]
requires: []
runtime: "<3s"
---

# 场景

同一函数注册两个 defer：后注册的 defer 先运行并 recover 原始 panic，先注册的 defer 随后运行。再用一层 caller defer 观察更外层 recover。验证 Go 的“一个 panic 只能被 recover 一次”语义，以及两层栈的真实闭包符号与行号。

## 期望行为

1. 内层 recover 到原始 `*mrPayload`，payload 身份与 next 链正确；
2. 外层 recover 返回 nil，调用方 wrapper 的 recover 也返回 nil；
3. 内层栈包含 `main.mrMulti.func2`、`main.mrMulti`、`main.mrMid`、`main.mrLeaf` 与 `main.go:<行号>`；
4. 外层栈包含 `main.mrMulti.func1`、`main.mrMulti` 与 `main.go:<行号>`；
5. GC 后 payload 仍有效；
6. 退出码 0，最后一行 `ALL PASS l14_multi_recover`。

## 失败判读

- 外层 recover 非 nil：recover 语义实现错误（同一 panic 被恢复两次）；
- 内层栈缺少 mid/leaf 帧：recover 时栈已提前回退，栈展开错误；
- 闭包符号不是 func1/func2：defer 闭包符号或行号归属错误；
- GC 后 payload 损坏：defer 帧栈映射漏标。

## 参数

无随机路径；所有函数 `//go:noinline`。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_multi_recover/*.go
GODEBUG=clobberfree=1 /tmp/case
```
