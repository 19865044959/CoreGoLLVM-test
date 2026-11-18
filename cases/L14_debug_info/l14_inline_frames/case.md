---
id: l14_inline_frames
dim: L14_debug_info
priority: P1
sources:
  - docs/DIMENSIONS.md: L14 调试信息（内联帧/行号）
tags: [L14, inline-frame, traceback, CallersFrames]
requires: []
runtime: "<3s"
---

# 场景

小函数 `smallOne→smallTwo→smallThree` panic 后，`debug.Stack()` 必须给出链路符号（内联与否都应可展开）；`runtime.CallersFrames` 在四层 `noinline` 调用中给出函数名、文件名与行号。

# 期望行为

1. panic 栈包含 `smallOne/smallTwo/smallThree`，payload 指针 GC 后有效；
2. `tinyC→tinyB→tinyA→inlineFrames` 的帧名齐全；
3. 所有帧行号 >0 且至少一帧属于 `main.go`；
4. 最后一行 `ALL PASS l14_inline_frames`。

# 失败判读

- 缺少中间函数：内联帧展开/行号表错误；
- 行号为 0 或文件缺失：调试信息生成缺失；
- 栈展开崩溃：栈映射/帧布局错误。

# 参数

无随机路径；是否真正内联由编译器决定，用例只要求符号可展开。

# 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_inline_frames/*.go
/tmp/case
```
