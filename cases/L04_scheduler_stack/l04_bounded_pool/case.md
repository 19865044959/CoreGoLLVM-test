---
id: l04_bounded_pool
dim: L04_scheduler_stack
priority: P2
sources:
  - tailscale@v1.80.2: syncs/syncs_test.go:145 容量 2 的 Semaphore 的 Acquire/TryAcquire/Release 与取消语义
  - pion/webrtc@v4.1.2: internal/mux/mux_test.go:111 有界 endpoint 队列满（ErrFull）非致命，后续包仍可分发
tags: [L04, bounded-pool, semaphore, backpressure, worker-pool, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_JOBS 可放大）"
---

# 场景

模拟 tailscale Semaphore 与 WebRTC mux 有界队列的并发池：默认 8 个 worker 消费容量 4 的 `jobs` channel，每个任务在 heavy 阶段前必须取得容量 3 的 `sem`。holder goroutine 先占满全部 sem 名额，使 8 个 worker 各持一个任务指针阻塞在 sem 入口；主协程收齐 8 个 waiting 信号后显式 GC 两次，再释放 sem。处理完成后 worker 再次阻塞在 finalRelease，主协程第二次 GC，最后校验并发峰值、任务唯一性、总和与 kept 指针链。

# 期望行为

1. 并发峰值在 1–3 之间，任何时刻都不超过 sem 容量 3；
2. 每轮 `TORTURE_JOBS` 个任务各被处理恰好一次，id 无缺失/重复；
3. 所有任务链的两节点差值为 9，最终 GC 后仍完整；
4. 每轮总和等于独立公式值；
5. 有界任务 channel 满时生产者阻塞但不丢任务；
6. 最后一行输出 `ALL PASS l04_bounded_pool`。

# 失败判读

- `max concurrent=... limit=...`：semaphore 容量失效，并发数失控；
- `job ... seen=... want=1`：任务丢失或重复处理；
- `job ... corrupted` / `missing after final GC`：任务指针在 GC 后损坏或被提前回收；
- `sum=...`：任务值错误；
- 超时：holder 占满 sem 后未释放，或 worker 在 waiting/finalReady 协调点死锁。

# 参数

- `UAF_SEED`：任务基值与轮次偏移种子（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–10；
- `TORTURE_JOBS`：每轮任务数，默认 96，范围 8–2048。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_bounded_pool/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=5 TORTURE_JOBS=512 GOGC=1 GOMAXPROCS=2 /tmp/case
```
