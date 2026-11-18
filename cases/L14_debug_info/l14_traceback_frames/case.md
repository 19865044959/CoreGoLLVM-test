---
id: l14_traceback_frames
dim: L14_debug_info
priority: P1
sources:
  - docs/DIMENSIONS.md: L14 调试信息（行号/栈展开）
tags: [L14, traceback, debug.Stack, runtime.Callers]
requires: []
runtime: "<3s"
---

# 场景

panic + recover 时用 `debug.Stack()` 抓栈，`runtime.CallersFrames` 抓调用帧，校验函数符号、文件名、行号；并在子 goroutine 的 panic 中重复。

# 期望行为

1. 栈中包含 `tbInner`/`tbMiddle`/`tbOuter`、`goroutine` 头、`main.go:<行号>`；
2. `runtime.CallersFrames` 至少 1 帧，包含 `callersInfo`，文件为 `main.go` 且行号 >0；
3. 子 goroutine panic 栈包含同样符号；
4. 最后一行 `ALL PASS l14_traceback_frames`。

# 失败判读

- 缺少函数名/文件/行号：行号表或 traceback 元数据生成错误；
- 行号为 0：调试信息缺失；
- 栈展开崩溃：帧指针/栈映射错误（与 L17 交叉）。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_traceback_frames/*.go
/tmp/case
```
