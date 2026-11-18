---
id: l17_named_return_defer
dim: L17_stackmap
priority: P0
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: A1/A2/A11（命名返回值、defer、deferreturn）
tags: [L17, defer, named-return, panic]
requires: []
runtime: "<2s"
---

# 场景

命名返回值被 defer 改写、defer 中触发 GC 并读取返回值、panic/recover 路径设置返回值。这些都是函数末尾/deferreturn 的栈映射边界，服务代码的 handler 与中间件大量使用。

# 期望行为

1. `namedBasic` 返回 defer 中新建的对象（`v=99,name=defer`）；
2. `namedDeferUses` 返回 `(r=5, n=12)`，即 defer 在 GC 后仍能读到 r；
3. `namedMulti(0/1)` 分别返回 100/200，n 均为 1；
4. `namedPanicRecover` 在 recover 中经 GC 后返回 `v=77` 的对象；
5. 20 轮稳定，最后一行 `ALL PASS l17_named_return_defer`。

# 失败判读

- 返回 nil 或值被改写：命名返回值所在栈槽在 GC 时未被标记；
- 仅在 clobberfree 下失败：返回值对象被提前回收；
- 崩溃：deferreturn/panic 展开路径的 stack map 错误。

# 参数

无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_named_return_defer/*.go
GODEBUG=clobberfree=1 /tmp/case
```
