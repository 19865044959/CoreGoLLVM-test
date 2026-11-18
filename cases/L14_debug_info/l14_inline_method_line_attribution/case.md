---
id: l14_inline_method_line_attribution
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_inline_method_line_attribution/main.go:31 - 可内联方法 mark 用 runtime.Caller(1) 记录调用点
  - cases/L14_debug_info/l14_inline_method_line_attribution/main.go:33 - 可内联方法 boom panic
  - cases/L14_debug_info/l14_inline_method_line_attribution/main.go:38 - 宿主 A 同一行完成 mark+boom
  - cases/L14_debug_info/l14_inline_method_line_attribution/main.go:41 - 宿主 B 的另一个调用点
  - cases/L14_debug_info/l14_inline_method_line_attribution/main.go:127 - 内联方法 Caller 行号断言
  - cases/L14_debug_info/l14_inline_method_line_attribution/main.go:152 - 两宿主行号不同
  - docs/DIMENSIONS.md:22 - L14 调试信息（内联行号归属）
tags: [L14, inline, method, runtime.Caller, line-number, traceback, gc]
requires: []
runtime: "<3s"
---

# 场景

小方法 `mark()`（内部 `runtime.Caller(1)` 记录调用点）与小方法 `boom()`（内部 panic）保持可内联；`ilHostA` 与 `ilHostB` 两个 `//go:noinline` 宿主都在**同一源码行**完成 `mark`+`boom`，但两宿主位于不同行。panic 后 recover 的 traceback 必须把 `boom` 显示为内联帧（`main.(*ilProbe).boom(...)`），宿主帧行号分别等于 `mark` 记录的调用点；在 main 的第三个调用点直接调用 `mark` 又得到不同行号。

## 期望行为

1. hostA 的 traceback 含 `main.(*ilProbe).boom(...)` 且恰好一次，含 `main.ilHostA(`、goroutine 头与 `main.go:<行号>`；
2. `CallersFrames` 中 `main.(*ilProbe).boom` 帧 File 以 `main.go` 结尾、Line>0，且排在 `main.ilHostA` 帧之前；
3. hostA 的 `mark` 行号 >0，与 hostA 帧 Line 相等；boom 行号小于 hostA 行号；
4. hostB 的 `mark` 行号与 hostA 不同，与 hostB 帧 Line 相等；boom 帧行号与 hostA 的 boom 行号相同（方法体行固定）；
5. hostB 帧 Line 与 hostA 帧 Line 不同；
6. main 中直接调用 `mark` 得到第三个不同行号，证明内联函数 `runtime.Caller` 归属到逻辑调用点；
7. 显式 GC 后 payload 链与记录行号有效；最后一行 `ALL PASS l14_inline_method_line_attribution`。

## 失败判读

- traceback 中 `boom` 无 `(...)` 标记：方法未被内联（构建参数/内联策略变化）；
- 宿主帧行号不等于 mark 记录值：内联函数的 Caller 行号归属错误或宿主调用语句被拆分到多行；
- 两宿主 boom 行号不同：同一内联方法的行号未复用其源码位置；
- 直接调用 mark 的行号与宿主之一相同：Caller 未按逻辑调用点归属。

## 参数

无随机路径。`mark`/`boom` 保持单行小函数以确保 Go 1.24.1 默认内联；两个宿主为 `//go:noinline`，避免宿主自身内联干扰观察。若需确认内联决策可用 `go build -gcflags=-m`。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_inline_method_line_attribution/*.go
GODEBUG=clobberfree=1 /tmp/case
# 可选：GOTOOLCHAIN=go1.24.1 go build -gcflags=-m ./cases/L14_debug_info/l14_inline_method_line_attribution/*.go
```
