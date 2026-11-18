---
id: l03_nested_goroutines
dim: L03_async_preempt
priority: P2
sources:
  - tailscale@v1.80.2: syncs/shardvalue_test.go:16 1000 goroutine × 10000 次原子分片计数长循环，穿插 GC
  - quic-go@v0.50.1: streams_map_outgoing_test.go:405 100 个 goroutine 并发 OpenStreamSync 与限流唤醒
tags: [L03, async-preempt, nested-goroutines, tight-loop, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认；TORTURE_ROUNDS 可放大）"
---

# 场景

外层 4 个 goroutine，每个再起 4 个内层 goroutine，形成两级嵌套。16 个内层 worker 各自在只读 `[]nNode` 上跑持针紧循环，步长与偏移由 worker 下标和 `UAF_SEED` 决定；所有 worker 完成默认迭代数（`1<<20`）后让出 P 等待并发 GC pump 至少完成一轮。默认规模为 P2 的快速档，`TORTURE_ROUNDS` 直接替换每个内层 worker 的迭代次数。

# 期望行为

1. 并发 GC pump 至少完成一轮；
2. 每个内层 worker 都达到默认迭代数；
3. 每个外层 worker 汇总的 4 个内层校验和，等于按固定迭代次数纯整数推算的参考值；
4. GC 后全部节点字段不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_nested_goroutines`；
6. 同一 `UAF_SEED` + `TORTURE_ROUNDS` 下结果一致。

# 失败判读

- `FAIL: GC pump completed during nested goroutines`：所有 worker 忙等期间 GC 未完成；
- `FAIL: nested worker checksums (seed=...)`：嵌套 worker 的指针读取或计数被破坏；
- `FAIL: nested-loop nodes intact after GC (seed=...)`：共享节点被回收或字段改写；
- 超时：嵌套 goroutine 未退出或与 GC 互锁。

# 参数

- `UAF_SEED`：决定各 worker 的偏移，默认 1，失败输出带出；
- `TORTURE_ROUNDS`：每个内层 worker 的迭代次数（默认 `1<<20`）；例如 `TORTURE_ROUNDS=4194304` 放大 4 倍。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_nested_goroutines/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 TORTURE_ROUNDS=4194304 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
