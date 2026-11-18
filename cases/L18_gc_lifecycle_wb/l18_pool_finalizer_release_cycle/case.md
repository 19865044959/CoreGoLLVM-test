---
id: l18_pool_finalizer_release_cycle
dim: L18_gc_lifecycle_wb
priority: P2
sources:
  - nats-server@v2.11.0: server/ipqueue_test.go:291（TestIPQueueRecycle：入队/出队/归还后 5 轮验证池化切片复用与 GC 淘汰）
  - nats-server@v2.11.0: server/norace_test.go:10072（TestNoRaceConnectionObjectReleased：finalizer + GC 轮询验证引用链断开）
tags: [L18, sync.Pool, finalizer, liveness, release-cycle, noinline, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 3 轮；TORTURE_ROUNDS 放大轮数，上限 16）"
---

# 场景

全局 `sync.Pool` 的对象在创建时设置 finalizer（只写按 id 索引的原子标记）。每轮：

1. noinline `acquireHeld` 从池中取出 64 个对象并写入当轮版本/payload，保存在全局 held；
2. 执行 512 次 Get/Put churn；
3. 两轮强制 GC 后断言：held 对象 finalizer 标记全为 0、payload 完整；
4. 轮末把 held 保留在全局切片，下轮继续。

所有轮次结束后，同时丢弃池引用与全局 held，轮询至所有创建过的对象（含仍留在池中的）finalizer 全部触发。仅断言“可达不终结 / 失活最终终结”。

# 期望行为

1. 每轮 churn + GC 后，64 个外部持有对象未被终结且 payload（ver/data[0]/data[1]）完整；
2. 所有轮次结束后、释放前，外部持有对象仍未被终结；
3. 同时丢弃池与外部引用后，所有对象在 5s 内最终被终结（无遗漏）；
4. 结果与 finalizer 具体触发时刻无关；
5. 退出码 0，最后一行 `ALL PASS l18_pool_finalizer_release_cycle`。

# 失败判读

- `held pooled objects corrupted`：池化对象 payload 被 GC 提前清除或被复用越界；
- `N held pooled objects finalized ...`：仍被外部引用的池化对象被误判死亡；
- `N pooled objects not finalized after pool and external refs released`：池/对象仍有隐藏引用，或 finalizer 未调度；先在官方 Go 1.24.1 复跑；
- 超时：finalizer 队列积压，可降低 `TORTURE_ROUNDS` 复测。

# 参数

- `TORTURE_ROUNDS`：轮数（默认 3，上限 16）；每轮 64 个持有对象 + 512 次 churn；
- 无随机路径；`UAF_SEED` 不影响结果。

# 运行

```bash
go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_pool_finalizer_release_cycle/main.go
TORTURE_ROUNDS=8 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
