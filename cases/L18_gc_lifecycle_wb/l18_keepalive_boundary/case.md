---
id: l18_keepalive_boundary
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - protobuf-go@v1.36.6: internal/impl/message_reflect_test.go:1493（unsafe 视图必须跨多轮 GC 保持底层对象内存有效）
tags: [L18, keepalive, liveness, finalizer, gc-boundary]
requires: []
runtime: "<10s"
---

# 场景

noinline `keepBoundary` 内创建带 finalizer 的 payload；字段读取是 GC 前最后一次显式使用，`runtime.KeepAlive(p)` 位于函数末尾。GC 与随后的 finalizer 轮询发生在“最后一次字段使用”和 KeepAlive 之间：

- 正确实现：p 必须活到 KeepAlive 处，finalizer 在边界前不得触发；
- 若编译器/运行时把 p 提前判死，GC 会回收对象并触发 finalizer，原子标记被置位。

函数返回后 p 真正失活，轮询 10s 等待 finalizer 触发。finalizer 只写原子标记，不读对象内容；断言仅涉及“可达不得终结 / 失活最终终结”。

# 期望行为

1. KeepAlive 边界前（含边界前的多次 GC 与 finalizer 轮询）标记保持 0；
2. 越过边界后对象最终被终结，标记变为 1；
3. 结果与 finalizer 的具体调度时刻无关（轮询有 10s 上限）；
4. 退出码 0，最后一行 `ALL PASS l18_keepalive_boundary`。

# 失败判读

- `object finalized before KeepAlive boundary`：KeepAlive 之前的对象存活区间被错误缩短（liveness/栈扫描/KeepAlive 语义问题）；
- `object was not finalized after KeepAlive boundary`：对象越过边界后仍被隐藏引用，或 finalizer 未被调度；先在官方 Go 1.24.1 复跑区分环境；
- 超时：finalizer 队列或 GC 饥饿，非致命，可重跑确认。

# 参数

无随机路径；边界前最多 20 次 GC 轮询，边界后 10s 上限。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_keepalive_boundary/main.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
