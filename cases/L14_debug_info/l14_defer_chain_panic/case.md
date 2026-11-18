---
id: l14_defer_chain_panic
dim: L14_debug_info
priority: P2
sources:
  - cases/L14_debug_info/l14_defer_chain_panic/main.go:85 普通函数作为 defer 直接 panic
  - cases/L14_debug_info/l14_defer_chain_panic/main.go:93 三层 defer 的 recover/重新 panic 链
  - cases/L14_debug_info/l14_defer_chain_panic/main.go:121 仅由普通 defer 函数触发的 panic
  - cases/L14_debug_info/l14_defer_chain_panic/main.go:130 每轮校验 LIFO 顺序与两个阶段的栈
  - docs/DIMENSIONS.md:22 L14 调试信息（defer 中 panic）
tags: [L14, defer, panic-in-defer, repanic, LIFO, traceback, TORTURE_ROUNDS, gc]
requires: []
runtime: "<10s（默认 1 轮；TORTURE_ROUNDS 放大轮数，上限 8）"
---

# 场景

函数内注册三个 defer（后注册先执行）：`func3` 先 recover 原始 panic、抓栈后再次 panic；`func2` 再 panic 覆盖 panic 值；`func1` 最后 recover 到最终值并抓栈。模拟清理函数在异常路径上连锁失败的现场。另覆盖普通函数（非闭包）作为 deferred call 直接 panic 的路径。所有阶段的 traceback 都必须带真实 defer 闭包/普通函数符号与 `main.go:行号`。`TORTURE_ROUNDS` 放大轮数。

## 期望行为

1. defer 执行顺序严格为 `recover-then-panic-p3` → `panic-p2` → `recover-last`；
2. `func3` recover 到的原始值等于 p1；`func1` recover 到的最终值等于 p2（后一次 panic 覆盖前值）；
3. `func3` 阶段栈包含 `main.deferChain.func3(`、`main.deferChain(`、`goroutine` 头与 `main.go:<行号>`；
4. 最终栈包含 `main.deferChain.func1(`、`main.deferChain.func2(`、`main.deferChain(` 与至少 2 处 `main.go:<行号>`；
5. 普通函数 defer panic 场景的栈包含 `main.panicOnCleanup(`、`main.deferOrdinary.func1(`、`main.deferOrdinary(` 与 `main.go:<行号>`；
6. 每轮 GC 后 payload 环形指针链仍有效；
7. 退出码 0，最后一行 `ALL PASS l14_defer_chain_panic`。

## 失败判读

- LIFO 顺序错误：defer 链执行顺序实现错误；
- 最终值不是 p2：panic 覆盖语义错误；
- 栈缺 `func2`/`func3`：defer 闭包帧未正确展开（与 L11 内联交互）；
- 普通函数帧缺失：deferred call 的 PC 行号归属错误；
- payload 失效：defer 帧栈映射漏标。

## 参数

- `TORTURE_ROUNDS`：轮数，默认 1，范围 1..8；
- `UAF_SEED` 不参与逻辑。

## 运行

```bash
go build -o /tmp/case ./cases/L14_debug_info/l14_defer_chain_panic/*.go
TORTURE_ROUNDS=4 GODEBUG=clobberfree=1 /tmp/case
```
