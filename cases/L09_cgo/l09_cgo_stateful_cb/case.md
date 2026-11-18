---
id: l09_cgo_stateful_cb
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:49（containerd 覆盖 L09，C 库事件/状态回调是常见边界）
  - docs/RUNBOOK.md:37（L09 依赖 cgo 能力标签与 C 工具链）
tags: [L09, cgo, callback, stateful, handle, export, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库“带状态回调”的绑定：C 侧折叠一组 `int32` 序列，每一步用整数 `handle` 回调 `//export goStateFold`，并把当前 accumulator 回传给 Go；Go 按 handle 在自己的状态表中查到 `mode/mult/bias`，计算新 accumulator、累加调用次数与参数和，回调内偶尔分配垃圾并显式 GC。C 只在本次调用内保存 accumulator，handle 只是整数键，因此不持有任何 Go 指针；与 plain callback 不同，本用例验证跨多步、多 handle 的状态隔离与累加器往返。

# 期望行为

1. 单 handle 折叠 100 个值，结果与 Go 参考实现一致，回调次数与参数和正确；
2. 一次 C 调用内交替使用两个 handle：accumulator 连续传递，但每一步按 handle 选择不同 Go 状态，结果与参考实现一致；
3. 两个 handle 的回调次数、参数和互不串扰；
4. 空输入不触发回调，结果保持 0；
5. 回调内 `runtime.GC()` 后状态表、状态指针、计数器仍正确；
6. GC 后继续回调，计数从原值累加；再次交错调用仍正确；
7. 最后一行 `ALL PASS l09_cgo_stateful_cb`。

# 失败判读

- `unknown callback handle`：C 传入的 handle 被截断/符号扩展，或 Go 状态表键类型不匹配；
- `fold ... result`：accumulator 在 C→Go→C 往返中丢失/错位；
- 计数串扰：两个 handle 的状态表项被错误合并，或 C 侧混用了局部状态；
- GC 时崩溃/计数丢失：回调帧栈映射或状态表被错误回收（本用例 C 不保存 Go 指针，排除跨调用悬挂）；
- 构建失败：`//export` 文件的 preamble 只能有声明，定义须留在 `stateful.c`。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。无额外环境变量。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_stateful_cb
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
