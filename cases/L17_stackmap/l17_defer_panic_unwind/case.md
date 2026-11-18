---
id: l17_defer_panic_unwind
dim: L17_stackmap
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: A11 defer/deferreturn/panic 展开
tags: [L17, defer, panic, recover, unwind]
requires: []
runtime: "<3s"
---

# 场景

1500 层递归后 panic，携带指针 payload；recover 中 GC 后读取 payload。另覆盖 defer 参数在 defer 时求值、循环内 defer 闭包捕获指针。

# 期望行为

1. `recoverDeep` 返回 `sum==1, ok==true`（panic payload 来自 depth=1 的局部变量）；
2. `useDeferArg` 返回 314；
3. `loopDefers` 返回 28（0..7 之和）；
4. 最后一行 `ALL PASS l17_defer_panic_unwind`。

# 失败判读

- sum 不对 / 崩溃：栈展开过程中栈帧指针未被正确扫描；
- payload 类型断言失败：panic 接口值（含指针）在展开时被破坏；
- 仅 clobberfree 下失败：被展开栈帧中的对象被提前回收。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_defer_panic_unwind/*.go
GODEBUG=clobberfree=1 /tmp/case
```
