---
id: l17_closure_defer_combo
dim: L17_stackmap
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37（100 个调度闭包，闭包内 defer + select）
  - fasthttp@v1.59.0: server_test.go:2914（TimeoutHandler 使用闭包 handler，select/time.After 收尾）
tags: [L17, stackmap, closure, defer, captured-pointer, per-iteration, GC]
requires: []
runtime: "<10s（默认 10 轮）"
---

# 场景

闭包与 defer 的组合：`newWorker` 返回的闭包捕获 `*ctx`，闭包体内 defer 再次读捕获指针；`closureDefers` 为 slice 每个元素创建捕获 per-iteration `*item` 的闭包；`deferredClosure` 把闭包直接作为 defer 目标并改写命名返回值。所有闭包对象在 GC 后仍能安全调用。

# 期望行为

1. 每个 worker 闭包在 GC 前后两次调用的结果等于 `x*scale + len(tag) + root.v`；
2. 每个 per-iteration 闭包返回 `2*list[i].v + root.v`；
3. `deferredClosure` 返回 `root.v + delta`；
4. 所有闭包执行后再次 GC，`ctx.root`/`ctx.list` 指针内容不变；
5. 最后一行 `ALL PASS l17_closure_defer_combo`，退出码 0。

# 失败判读

- `worker: captured ctx corrupted`：闭包 capture 槽未被扫描；
- `list closure` 结果错误：per-iteration 捕获的指针被回收或错位；
- `deferred closure` 结果错误：defer 闭包读取命名返回值失败；
- 仅 clobberfree/efence 下失败：闭包捕获对象提前释放；
- 崩溃：funcval 中的捕获指针未被 GC 扫描。

# 参数

- `UAF_SEED`：决定 list 长度与基数；失败信息带种子。
- 无 TORTURE 参数。

# 运行

```bash
go build -o /tmp/case ./cases/L17_stackmap/l17_closure_defer_combo/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
