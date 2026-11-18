---
id: l18_keepalive_multi_objects
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（TestUnsafeAssumptions：unsafe 视图必须跨多轮 GC 保持底层对象内存有效）
  - go-redis@v9.8.0: race_test.go:107（大 value 并发读取，重连/GC 后继续读）
tags: [L18, keepalive, liveness, finalizer, interface, slice, loop]
requires: []
runtime: "<10s"
---

# 场景

用 finalizer 作为存活探针，覆盖三种 `runtime.KeepAlive` 边界：

1. 切片边界：noinline 创建 8 个带 finalizer 的对象放入 `[]*kaObj`，逐字段读取后执行 GC + 20 次轮询，`runtime.KeepAlive(objs)` 位于函数末尾；
2. 接口边界：对象只通过 `interface{}` 可达，类型断言读取字段后 GC + 轮询，`runtime.KeepAlive(v)` 在末尾；
3. 循环边界：每轮新建对象、末次字段使用后立即双 GC，断言标记仍为 0，再 `runtime.KeepAlive(o)`。

三个边界函数返回后对象真正失活，主协程轮询至所有 finalizer 触发。finalizer 只写原子标记，不读对象内容；仅断言“可达不终结 / 失活最终终结”。

# 期望行为

1. 切片中 8 个对象在 KeepAlive 之前的 GC 与轮询窗口内均未被终结；
2. 仅由 interface 持有的对象在边界前未被终结；
3. 循环内每个对象在自身 KeepAlive 前未被终结；
4. 边界函数返回后，所有对象在 5s 内最终被终结；
5. 退出码 0，最后一行 `ALL PASS l18_keepalive_multi_objects`。

# 失败判读

- `... finalized before KeepAlive boundary`：对象存活区间被错误缩短（liveness/栈扫描/KeepAlive 语义问题）；
- `... finalized at KeepAlive boundary`：KeepAlive 未能延长生命周期；
- `N objects not finalized after KeepAlive boundaries`：边界后仍存在隐藏引用或 finalizer 未调度；先在官方 Go 1.24.1 复跑区分环境；
- 超时：finalizer 队列或 GC 饥饿，可重跑确认。

# 参数

无随机路径；`UAF_SEED` 不影响结果。固定 8+1+4 个对象，每段边界轮询上限 20 次，结束后 finalizer 轮询上限 5s。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_keepalive_multi_objects/main.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
