---
id: l09_cgo_callback
dim: L09_cgo
priority: P1
sources:
  - docs/DIMENSIONS.md:17（L09 cgo）
  - docs/research/tailscale.md:68（cgo/内核边界测试需真实 C 路径）
tags: [L09, cgo, callback, export, function-pointer, GC]
requires: [cgo]
runtime: "<5s"
---

# 场景

模拟 C 库回调 Go 的典型绑定（事件回调/排序比较器）：C 侧把 `//export goCallback` 的地址存入函数指针 `cb_t`，在 C 调用期内同步回调；回调更新 Go 全局计数、分配垃圾，并每 16 次调用执行一次 `runtime.GC()`。C 不保留函数指针或 Go 指针到调用返回之后。

# 期望行为

1. 50 次单回调：结果为 `i*3+1`，回调计数与最后参数逐次正确；
2. 一次 C 循环内 5 次回调，总和为 `(10+i)*3+1` 之和，计数达到 55；
3. 回调内的 GC 不破坏回调状态（`cbCalls/cbLast/cbSum` 正确）；
4. GC 之后再次回调 8 次仍正确（C 每次调用重新取函数指针地址）；
5. 最后一行 `ALL PASS l09_cgo_callback`。

# 失败判读

- 构建失败提示 preamble 含定义：`//export` 文件的 preamble 只允许声明，C 函数定义放在 `callback.c`；
- 回调结果错误：`//export` 签名与 C 声明不一致（int32_t）；
- GC 时崩溃：回调帧栈映射/寄存器扫描问题，或异步抢占进入无栈映射汇编（本用例无手写汇编）；
- 计数不符：C 侧函数指针被错误保留/覆盖，或回调被多次注册。

# 参数

需要 `CGO_ENABLED=1` 与 C 工具链；`BUILD_MODE=module`。`callback.c` 与 `main.go` 同包编译。

# 运行

```bash
cd cases/L09_cgo/l09_cgo_callback
GOTOOLCHAIN=go1.24.1 CGO_ENABLED=1 go build -o /tmp/case . && GODEBUG=clobberfree=1 /tmp/case
```
