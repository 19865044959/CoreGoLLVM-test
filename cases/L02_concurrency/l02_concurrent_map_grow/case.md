---
id: l02_concurrent_map_grow
dim: L02_concurrency
priority: P2
sources:
  - tailscale@v1.80.2: syncs/shardedmap_test.go:8 16 shard 泛型 map 的 Set/Delete/Mutate 三态
  - go-redis@v9.8.0: ring_test.go:143 动态缩/扩容 shard，复用旧 shard、关闭多余 shard
  - kitex@v0.13.0: pkg/remote/trans/netpollmux/shard_map_test.go:27 32 分片 map store/load/delete 与 range 计数
tags: [L02, sharded-map, resize, rehash, concurrent-growth, rwlock, monotonic-count, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 5×48 次写；TORTURE_ROUNDS 放大写/读轮次，上限 16）"
---

# 场景

模拟分片并发容器在负载增长时动态扩容：初始 4 个 shard，条目数超过 `shards*8` 时倍增并搬迁全部 key（go-redis ring rehash、tailscale/kitex sharded map 的规模变化路径）。写入持有 resize 读锁 + shard 锁，扩容持有 resize 写锁，保证迁移期间不丢更新；读者并发读取并校验计数单调。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_concurrent_map_grow`；
2. 200 个 key 全部命中，最终分片数为 32（由 200 条与负载因子 8 唯一确定）；
3. 每个 key 的计数等于串行期望，总和等于总写入次数；
4. 迁移期间读者观察到的计数不倒退；最终表无重复 key、元数据与校验和自洽；
5. GC 后条目与计数不变。

## 失败判读

- `key N count=... want=...` 或 `total count` 不符：扩容拷贝期间写丢失/重复，检查 resize 读写锁与 shard 锁顺序；
- `final shards != 32`：倍增阈值或最后一次触发条件错误；
- `count regressed`：读者看到了旧表/新表混合的过期值；
- 超时/死锁：resize 写锁与 shard 锁获取顺序不一致。

## 参数

- `UAF_SEED`：决定条目元数据 tag，失败信息带种子；
- `TORTURE_ROUNDS`：放大每 worker 写次数与读者轮次（默认 1，上限 16）；key 空间固定 200，故最终分片数仍确定。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_concurrent_map_grow/*.go
UAF_SEED=1 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
