---
id: l02_atomic_map_swap
dim: L02_concurrency
priority: P2
sources:
  - frp@v0.67.0: pkg/util/metric/counter_test.go:9 原子计数 + 快照/清零
  - caddy@v2.10.0: modules/caddyhttp/metrics_test.go:30 in-flight gauge 归零与快照一致性
  - nats-server@v2.11.0: server/sublist_test.go:957 不可变快照 + atomic.Pointer 发布，读侧无锁
tags: [L02, atomic-counter, map-swap, atomic.Pointer, immutable-snapshot, generation, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 6×300 次自增；TORTURE_ROUNDS 放大 ops/swaps/读取轮次）"
---

# 场景

模拟 frp 原子计数快照与 caddy in-flight gauge：6 个 worker 只对 16 个 `atomic.Int64` 分片计数器和全局 total 做原子自增（seed 决定 key 偏移），不加锁。

独立单写者周期性构建完整 `map[int]int64` 快照并用 `atomic.Pointer[snap]` 交换；4 个读者无锁 Load，校验：map 恰有 16 个固定 key、每值在终值上界内、sum 字段等于 map 求和、同一读者观察到的代 gen 不回退。写者每 8 次交换、读者每 29 次读取显式 GC 并重校验当前快照。worker 全部结束后再交换一次最终快照。

# 期望行为

1. 任意时刻读者看到的快照自洽（keys 完整、sum 匹配、各值不超过对应终值）；
2. 单写者发布下，同一读者观察的 gen 单调不减；
3. 全部 worker 结束后 total 与 16 个分片计数器精确等于按调度模拟的期望值；
4. 最终快照 gen 为 `swaps+1`，内容与计数器逐 key 相等，GC 后不变；
5. 最后一行 `ALL PASS l02_atomic_map_swap`。

# 失败判读

- `snapshot generation regressed`：atomic.Pointer 发布顺序或读者状态错误；
- `reader observed inconsistent snapshot`：读到半构建 map（发布前未完成写入）；
- `counter` / `final snapshot key`：原子计数丢更新或 map 交换丢数据；
- `snapshot corrupted after GC`：快照 map 或指针在 GC 后失效。

# 参数

- `UAF_SEED`：决定 key 偏移与失败输出；
- `TORTURE_ROUNDS`：放大 ops、swap 数与读者轮次，范围 1..50，默认 1。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_atomic_map_swap/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
