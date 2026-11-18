---
id: l17_method_named_defer
dim: L17_stackmap
priority: P0
sources:
  - nats-server@v2.11.0: server/filestore_test.go:3823（fileStore.reportMeta 命名返回 + defer RUnlock）
  - quic-go@v0.50.1: send_stream_test.go:30（writerWithTimeout.Write 方法命名返回，写路径穿插 GC）
tags: [L17, stackmap, method, named-return, defer, deferreturn, method-value, GC]
requires: []
runtime: "<3s（默认 10 轮）"
---

# 场景

方法级命名返回 + defer 的边界：指针 receiver `tracker.run` 有 3 个 defer（闭包、方法值 `t.stamp`、带实参闭包），执行顺序 LIFO；每个 defer 内外都 GC，并读取命名返回值、receiver 与 defer 时求值的实参。另覆盖 `(*fixer).retag` 在 defer 中覆盖命名指针返回值，以及值接收者 `valTracker.calc` 的命名返回被 defer 闭包读取。

# 期望行为

1. `run` 返回的 `out` 跨 defer 内 GC 后仍为 `final`，`total` 等于 body + 两个 defer 的累加；
2. `t.ops == [3 2 1]`，证明 defer LIFO 与实参求值时机；
3. `retag` 返回 defer 中新建的 `defer-win` 对象；`calc` 的命名返回与 n 正确；
4. receiver/tracker/valTracker 的指针字段在多次 GC 后不变；
5. 最后一行 `ALL PASS l17_method_named_defer`，退出码 0。

# 失败判读

- `run: named return out corrupted`：命名返回值栈槽未被扫描；
- `defer order` 不符：defer 链本身异常（与 GC 无关的环境问题）；
- `calc/value receiver` 失败：值接收者副本中的指针未被标记；
- 仅 clobberfree 下失败：defer 记录/返回槽中的对象被提前回收；
- 崩溃：deferreturn/方法值 wrapper 的栈映射错误。

# 参数

- `UAF_SEED`：决定 tag 偏移；失败信息带种子。
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_method_named_defer/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
