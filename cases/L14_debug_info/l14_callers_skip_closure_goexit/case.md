---
id: l14_callers_skip_closure_goexit
dim: L14_debug_info
priority: P1
sources:
  - cases/L14_debug_info/l14_callers_skip_closure_goexit/main.go:82 - skCapture 同时用整栈与单槽 PC 缓冲测试 skip
  - cases/L14_debug_info/l14_callers_skip_closure_goexit/main.go:94 - 方法值目标 (*skWorker).leaf
  - cases/L14_debug_info/l14_callers_skip_closure_goexit/main.go:99 - skEntry 内闭包 inner 经方法值调用
  - cases/L14_debug_info/l14_callers_skip_closure_goexit/main.go:119 - 子 goroutine 命名入口 skGo
  - cases/L14_debug_info/l14_callers_skip_closure_goexit/main.go:157 - skip 0..6 期望逻辑帧序列
  - docs/DIMENSIONS.md:22 - L14 调试信息（Callers skip/栈展开）
tags: [L14, Callers, skip, closure, method-value, goexit, gc]
requires: []
runtime: "<3s"
---

# 场景

子 goroutine 入口 `skGo` 调用 `skEntry`；`skEntry` 内定义闭包 `inner`，`inner` 通过方法值 `mv := w.leaf` 调用 `(*skWorker).leaf`，最终在 `skCapture` 中执行 `runtime.Callers(skip, ...)`。skip=0..8 逐层外移，skip 必须把闭包与方法值当逻辑帧计数，最后到达 goroutine 栈底 `runtime.goexit`。同时用单槽 PC 缓冲验证 skip 语义不受缓冲截断影响，并在第二个 goroutine 上重复验证稳定性。

## 期望行为

1. skip=0..6 的首帧依次为 `runtime.Callers`、`main.skCapture`、`main.(*skWorker).leaf`、`main.skEntry.func1`、`main.skEntry`、`main.skGo`、`runtime.goexit`；
2. skip=k 时帧数为 7-k，末帧为 `runtime.goexit` 且行号 >0；
3. skip=7 与 skip=8 返回 0 帧；单槽缓冲在 skip=0..6 下仍给出同样首帧，第 7 个为 `runtime.goexit`；
4. skip=3 的帧链中每个 main 帧 File 以 `main.go` 结尾、Line>0，Entry 能解析到同符号或其闭包宿主（内联闭包场景）；
5. 捕获帧中不出现 `-fm` 方法值包装逻辑帧；
6. 两个不同 goroutine 上同一 skip 的函数序列与行号完全一致；
7. 显式 GC 后 payload 链有效，最后一行 `ALL PASS l14_callers_skip_closure_goexit`。

## 失败判读

- skip=2 首帧不是 `main.(*skWorker).leaf`：方法值多出/缺少逻辑帧；
- skip=3 首帧不是闭包符号：闭包帧未计入 skip；
- 末帧不是 `runtime.goexit` 或越过 goexit 仍返回帧：goroutine 栈底归属/skip 计数错误；
- 单槽缓冲下序列改变：skip 与 PC 缓冲交互错误；
- 两次 goroutine 行号不同：行号表不稳定。

## 参数

无随机路径。链上函数与 `skGo` 均 `//go:noinline`；断言依赖精确符号名与 skip 相对关系，不依赖具体行号值。

## 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L14_debug_info/l14_callers_skip_closure_goexit/*.go
GODEBUG=clobberfree=1 /tmp/case
```
