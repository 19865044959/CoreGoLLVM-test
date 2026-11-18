---
id: l18_finalizer_queue_drain
dim: L18_gc_lifecycle_wb
priority: P1
sources:
  - nats-server@v2.11.0: server/norace_test.go:10072（TestNoRaceConnectionObjectReleased：连接对象 finalizer + GC 轮询）
  - badger@v4.5.0: db_test.go:1246（TestGetSetRace：无缓冲 chan 同步交接后读侧做值校验）
tags: [L18, finalizer, queue-channel, reachability, gc-poll, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

finalizer 只把对象 id 投递到容量 2048 的有界 channel，不读 payload。分四阶段：

1. 192 个对象由全局 slice 持有，经 20 轮 GC + 轮询窗口，队列必须始终为空；
2. noinline 内创建 192 个立即失活的对象，GC 后轮询至 192 个 id 全部入队；断言无遗漏、无非预期 id；
3. 64 对“可达父 + 立即失活父共享同一个子”，子的 finalizer 在可达父存活期间不得入队；
4. 释放全局 slice 与共享父，所有被释放对象最终全部入队；断言每个 finalizer 只投递一次、无队列溢出。

# 期望行为

1. 全局可达对象在 GC 窗口内 `finalizer 标记 == 0` 且队列为空；
2. 失活对象在 5s 上限内全部入队，且队列中不出现可达对象 id；
3. 共享子在可达父存活时不被终结，释放父后最终被终结；
4. 无重复投递、无队列溢出、无非法 id；
5. 失败信息带 `seed=<UAF_SEED>`；
6. 退出码 0，最后一行 `ALL PASS l18_finalizer_queue_drain`。

# 失败判读

- `reachable objects were queued`：存活判定错误，可达对象被误判死亡；
- `dead objects not queued`：finalizer 未运行或对象被错误保活；
- `duplicate finalizer deliveries`：对象被复活后又执行 finalizer（本用例不应发生）；
- `overflow`：队列过小或 finalizer 异常重复投递；
- 本用例只断言“可达不终结 / 失活最终终结”，不依赖 finalizer 的触发顺序或精确时刻。

# 参数

- `UAF_SEED`：轮询间隔与数据模式种子（1–3ms），失败输出带出；
- 无 TORTURE 参数；等待上限固定 5s。

# 运行

```bash
GOTOOLCHAIN=go1.24.1 go build -o /tmp/case ./cases/L18_gc_lifecycle_wb/l18_finalizer_queue_drain/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
