---
id: l14_closure_lineinfo
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_closure_lineinfo/main.go:63 三个闭包各自报告 runtime.Caller 行号
  - cases/L14_debug_info/l14_closure_lineinfo/main.go:80 panic 闭包 debug.Stack
  - docs/DIMENSIONS.md:22 L14 调试信息（行号表）
tags: [L14, closure, line-info, runtime.Caller, debug.Stack, gc]
requires: []
runtime: "<3s"
---

# 场景

在同一 noinline 工厂函数中定义三个顺序排列的闭包，每个闭包用 `runtime.Caller(0)` 报告自己的文件/行号/符号；闭包同时捕获一个指针用于 GC 校验。另有一个 panic 闭包，用 `debug.Stack()` 验证闭包帧符号与 `main.go:<行号>`。

## 期望行为

1. 三个闭包报告的文件均以 `main.go` 结尾、行号 > 0；
2. 符号分别为 `main.makeLineClosures.func1/2/3`；
3. 行号严格递增（`line1 < line2 < line3`），只校验相对顺序，不依赖具体值；
4. 三个闭包捕获的指针值在 GC 后依次为 7/8/9；
5. panic 闭包栈包含 `main.panicClosureFactory.func1`、`main.runClosureRecover` 与 `main.go:<行号>`；
6. 退出码 0，最后一行 `ALL PASS l14_closure_lineinfo`。

## 失败判读

- 闭包符号不含 `.func1/2/3` 或行号为 0：闭包调试信息/行号表错误；
- 行号未递增：行号归属错乱（常见于闭包内联的 line table bug）；
- 捕获指针值错误：闭包环境变量的栈映射漏标。

## 参数

无随机路径。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_closure_lineinfo/*.go
GODEBUG=clobberfree=1 /tmp/case
```
