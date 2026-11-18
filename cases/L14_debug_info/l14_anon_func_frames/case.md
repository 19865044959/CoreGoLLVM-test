---
id: l14_anon_func_frames
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_anon_func_frames/main.go:57 IIFE 内 panic
  - cases/L14_debug_info/l14_anon_func_frames/main.go:62 匿名函数作为参数间接调用
  - cases/L14_debug_info/l14_anon_func_frames/main.go:78 匿名函数内部抓自身帧
  - cases/L14_debug_info/l14_anon_func_frames/main.go:93 函数表/参数/IIFE 三种匿名函数形态
  - docs/DIMENSIONS.md:22 L14 调试信息（栈展开/行号）
tags: [L14, anonymous-function, IIFE, func-literal, traceback, CallersFrames, gc]
requires: []
runtime: "<3s"
---

# 场景

覆盖三种匿名函数形态：helper 内立即执行的 IIFE、作为参数传入的匿名函数、放在函数表中的两个匿名函数字面量。分别用 `debug.Stack` 与 `runtime.CallersFrames` 验证匿名帧的真实 `.funcN` 符号、文件与行号。

## 期望行为

1. IIFE 路径 recover 到同一 payload，栈包含 `main.anonIIFE.func1`、`main.anonIIFE`、`main.anonInvoke`、`main.anonRun` 与 `main.go:<行号>`；
2. 匿名参数内部抓到的第 2 帧（第 1 帧为 anonFrames 自身）Function 前缀为 `main.main.func`，File 以 `main.go` 结尾、Line>0，且 `runtime.FuncForPC(Entry).Name()` 与帧名一致；
3. 函数表中两个不同字面量的 funcval 都有真实 `main.main.func` 符号且互不相同；
4. 每次间接调用都 recover 到同一 payload，GC 后 payload 链有效；
5. 退出码 0，最后一行 `ALL PASS l14_anon_func_frames`。

## 失败判读

- 匿名帧显示为 `main.main` 或调用者：匿名函数符号生成错误；
- funcval 名称不以 `.func` 结尾：匿名函数符号表缺失；
- Entry 名称与帧名不一致：行号/符号映射错误；
- 行号为 0：调试信息缺失。

## 参数

无随机路径；helper 均 `//go:noinline`。匿名函数编号由源码顺序决定，断言只要求真实 `.func` 前缀，不依赖具体编号值。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_anon_func_frames/*.go
GODEBUG=clobberfree=1 /tmp/case
```
