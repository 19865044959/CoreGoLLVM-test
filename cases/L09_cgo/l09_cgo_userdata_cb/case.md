---
id: l09_cgo_userdata_cb
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo：C 调用约定、指针传递）
  - docs/SOURCES.md:54（tailscale 覆盖 L09，C 回调边界）
  - docs/research/tailscale.md:68（cgo/内核边界需真实 C 调用路径）
tags: [L09, cgo, userdata, callback, export, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 事件 API 的 userdata 模式：C calloc 两个上下文 `{calls,sum,mul}`，每次调用把同一指针经 `void *` 传给 `//export goUDApply`；回调累加状态、按 mul 返回结果，每 32 次分配垃圾并 GC。两个上下文交替使用，验证隔离。

## 期望行为

1. C userdata 大小与 Go 布局一致（3×int64）；
2. 单次回调结果=参数×mul，calls/sum 各自更新且互不串扰；
3. C 循环内 5 次回调传入同一 userdata，返回和与 calls/sum 正确；
4. 两个上下文交替 200 次（回调内 GC），calls/sum 分别正确；
5. GC 后继续回调结果正确；free 后进程正常退出；
6. 最后一行 `ALL PASS l09_cgo_userdata_cb`。

## 失败判读

- 构建失败：`//export` 文件 preamble 只允许声明，C 定义放在 userdata.c；
- 回调读错上下文：userdata 指针类型/传递路径错误；
- 状态串扰：C 复用了错误的 userdata 或 Go 侧使用全局状态；
- GC 时崩溃：回调帧栈映射问题，或 C 在调用结束后仍保留 userdata。

## 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。 不依赖 `UAF_SEED`（无随机路径）；`CAPS=cgo` 可过滤本用例。

## 运行

```bash
cd cases/L09_cgo/l09_cgo_userdata_cb
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case .
GODEBUG=clobberfree=1 /tmp/case
```
