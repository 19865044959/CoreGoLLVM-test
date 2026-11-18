---
id: l02_lru_sharded_concurrent
dim: L02_concurrency
priority: P2
sources:
  - gorm@v1.30.0: tests/lru_test.go:350 1000 goroutine 并发 Add 100 个 key，最终 Len()==100
  - tailscale@v1.80.2: net/flowtrack/flowtrack_test.go:15 MaxEntries=2 的 LRU，超限淘汰最旧条目
  - fiber@v2.52.6: middleware/cache/cache_test.go:712 MaxBytes=2 时按插入/访问顺序 LRU 淘汰
tags: [L02, LRU, sharded-lock, container/list, recency-promotion, eviction, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 8 worker × 300 次操作；TORTURE_ROUNDS 放大操作数，上限 8 倍）"
---

# 场景

与 `l02_rwmutex_cache_evict`（单锁 + TTL 过期淘汰）不同，本用例覆盖**分片锁 + 读命中提升 recency** 的并发 LRU：4 个 shard 各自持有独立 mutex、`map` 与 `container/list`，读命中 `MoveToFront`，写满后从队尾淘汰。

单线程阶段精确验证 LRU 顺序：容量 4 的 shard 放入 0/4/8/12，访问 0 后再插入 16 必须淘汰 4；访问 8 后再插入 20 必须淘汰 12。并发阶段 8 个 worker 按 `UAF_SEED` 驱动的确定性随机序列执行 get/put（miss 时回填），周期性 GC，最终校验每个 shard 不超容量、map/list 索引一致、payload 完整、get 记账守恒。

# 期望行为

1. 单线程阶段按访问顺序精确淘汰 key 4 与 12，其余 key 命中；
2. 并发阶段结束后总条目数在 `(0, shards*capacity]` 内；
3. 每个 shard 的 `len(map) == len(list)` 且不超过容量；
4. 所有存活 payload 的 tag/val/data 一致，无撕裂；
5. `hits + misses == gets`；
6. 最后一行 `ALL PASS l02_lru_sharded_concurrent`。

# 失败判读

- `LRU must evict...`：recency 提升或淘汰端错误；
- `shard over capacity` / `map/list mismatch`：并发场景下淘汰与索引更新不一致；
- `cached payload corrupted`：被淘汰对象被复用/悬空，或 payload 被 GC 破坏；
- `get accounting mismatch`：并发读写漏计数；
- 超时：分片锁死锁或 GC 饥饿。

# 参数

- `UAF_SEED`：决定每个 worker 的操作序列与 payload 派生值；
- `TORTURE_ROUNDS`：放大每 worker 操作数（默认 300，最大 8 倍）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_lru_sharded_concurrent/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4 UAF_SEED=2 /tmp/case
```
