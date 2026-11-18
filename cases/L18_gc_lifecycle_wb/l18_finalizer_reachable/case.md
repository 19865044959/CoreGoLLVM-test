---
id: l18_finalizer_reachable
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - GC_STACKMAP_UAF_RISK_AND_TEST_PLAN.md: 漏标探针（finalizer）
tags: [L18, finalizer, liveness, probe]
requires: []
runtime: "<10s"
---

# 场景

用 finalizer 作为"对象是否被误判死亡"的探针：一个由全局变量持有的对象，finalizer 绝不能执行；另一个立即失活的对象，finalizer 必须在超时内执行。

# 期望行为

1. 可达对象在 3 次强制 GC 后 `finKept == 0`；
2. 失活对象在 10s 内 `finDropped == 1`；
3. finalizer 读到的对象内容未损坏（`v/name` 正确）；
4. 最后一行 `ALL PASS l18_finalizer_reachable`。

# 失败判读

- `finalizer ran while object reachable` / `kept object finalized prematurely`：对象仍可达却被判死，典型 stackmap 漏标；
- `dropped object finalizer did not run`：finalizer 机制/调度异常（先与官方 go 对照，排除运行时差异）；
- `finalizer saw corrupted payload`：对象被提前回收后 finalizer 读到垃圾。

# 参数

无随机路径；超时 10s 固定。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_finalizer_reachable/*.go
GODEBUG=clobberfree=1 /tmp/case
```
