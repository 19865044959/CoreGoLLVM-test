---
id: l18_finalizer_graph_cohorts
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:10072（TestNoRaceConnectionObjectReleased：全部连接对象设 finalizer，关闭后循环 GC 等待触发）
  - pgx@v5.7.4: pgxpool/pool_test.go:126（TestPoolAcquireAndConnHijack：连接从池中摘除、生命周期归属转移）
tags: [L18, finalizer, parent-child, liveness, shared-child, noinline]
requires: []
runtime: "<10s"
---

# 场景

父对象与子对象都设置 finalizer，finalizer 只写按 id 索引的原子标记，不读取对象内容。分三组：

1. 48 对全局可达的父+子（noinline 创建，随后挂到全局切片）；
2. 24 对“共享子”：可达父与立即失活的父引用同一个子；
3. 64 对在 noinline 内创建后立即失活的父+子。

先对可达组做多轮 GC + finalizer 轮询窗口，再对失活组轮询至 finalizer 全部触发，最后复查可达组。仅断言“可达不得终结 / 失活最终终结”两类属性，不依赖 finalizer 触发顺序或时刻。

# 期望行为

1. 全局可达的 48 个父与 48 个子在 GC 窗口内 `finalizer 标记 == 0`；
2. 24 个可达共享父的标记为 0；
3. 64 个失活父与 64 个非共享失活子最终全部被终结（上限 5s 轮询）；
4. 失活共享父被终结后，其子仍被可达父持有，标记必须保持 0；
5. 退出码 0，最后一行 `ALL PASS l18_finalizer_graph_cohorts`。

# 失败判读

- `reachable parent/child finalized prematurely`：仍可达对象被误判死亡，典型为根扫描/栈映射问题；
- `unreachable parents/children not finalized`：引用链未断开或 finalizer 未调度；先在官方 Go 1.24.1 复跑，区分运行时差异；
- `shared child finalized while still reachable` / `after dead parent was collected`：子对象只有一个失活父引用时被错误回收，或多个父共享时存活判定错误；
- 超时：finalizer 队列积压，可重跑确认。

# 参数

无随机路径；`UAF_SEED` 不影响结果。固定 48 可达对 + 24 共享对 + 64 失活对，失活轮询上限 5s。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_finalizer_graph_cohorts/main.go
GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
