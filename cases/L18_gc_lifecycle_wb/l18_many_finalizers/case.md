---
id: l18_many_finalizers
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - nats-server@v2.11.0: server/norace_test.go:10072（对全部连接对象设置 finalizer，关闭后每 10ms GC 等待触发）
tags: [L18, finalizer, liveness, mass-objects, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 2×1024；TORTURE_ROUNDS 放大批次数，上限 16）"
---

# 场景

每轮创建两批带 finalizer 的对象：

- 1024 个对象追加到全局 `kept`，进程内始终可达；
- 1024 个对象在 noinline 函数内创建后立即失活，仅记录 id。

批量创建后强制 GC，然后轮询到所有失活对象 finalizer 触发（上限 10s）。之后多轮 GC 再检查可达批次的 finalizer 标记始终为零。finalizer 只写一个按 id 索引的原子标记，不读对象内容。

# 期望行为

1. 所有失活对象最终都被终结（轮询结果为零遗漏）；
2. 所有仍被全局切片持有的对象在多次 GC 后都未被终结；
3. 失败输出只报告上述两类计数，不依赖 finalizer 的具体触发时序；
4. 退出码 0，最后一行 `ALL PASS l18_many_finalizers`。

# 失败判读

- `N unreachable objects were not finalized`：finalizer 未触发；先在官方 Go 1.24.1 复跑，排除运行时/finalizer 队列差异；
- `N reachable objects were finalized`：仍可达对象被误判死亡并终结，典型 stackmap 漏标或根扫描错误；
- 超时：finalizer 队列积压或 GC 饥饿；可降低 `TORTURE_ROUNDS` 复测。

# 参数

- `TORTURE_ROUNDS`：批次数（默认 2，上限 16）；每批 1024 个可达 + 1024 个失活对象；
- 无随机路径。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_many_finalizers/main.go
TORTURE_ROUNDS=8 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
