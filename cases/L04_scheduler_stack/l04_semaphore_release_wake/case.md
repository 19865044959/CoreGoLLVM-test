---
id: l04_semaphore_release_wake
dim: L04_scheduler_stack
priority: P2
sources:
  - tailscale@v1.80.2: syncs/syncs_test.go:145 容量 2 的 Semaphore：Acquire/TryAcquire/Release 与已取消 ctx 的 AcquireContext
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 100 个等待者按限流放行，其中随机取消者从等待 map 移除且不占名额
tags: [L04, semaphore, waiter-queue, cancel, FIFO, wakeup, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_WAITERS 可放大）"
---

# 场景

手写容量 C 的 FIFO 信号量：main 先占满 C 个名额，M=12 个 waiter 排队。全部入队后 GC，随后取消其中 K=2 个（关闭 done），取消者必须从队列摘除且不消耗名额；再逐个 release C 次，名额沿队列依次交接唤醒。每个 waiter 获得名额后校验私有链并归还。

# 期望行为

1. 每个非取消 waiter 恰好获得一次名额，取消者永不获得；
2. 授予顺序与取消后的队列顺序（FIFO）完全一致；
3. 并发持有者峰值不超过容量 C；
4. 结束后 free 名额数恢复为 C，取消者不泄漏名额；
5. 排队期间与结束后的 GC 均不破坏保留链；
6. 最后一行输出 `ALL PASS l04_semaphore_release_wake`。

# 失败判读

- 取消者出现在授予列表：取消路径未从队列摘除，或摘除后名额被吞；
- 授予顺序非 FIFO：交接唤醒顺序错乱或队列操作有竞态；
- max active > cap：release 未与 acquire 配对，导致名额超发；
- free != cap：名额泄漏或重复归还；
- 超时：release 未唤醒队首或取消摘除逻辑死锁。

# 参数

- `UAF_SEED`：链基值（默认 1）；
- `TORTURE_ROUNDS`：轮数（默认 2，范围 1..12）；
- `TORTURE_WAITERS`：waiter 数（默认 12，范围 4..32）；
- `TORTURE_CAPACITY`：信号量容量（默认 3，范围 1..8）；
- `TORTURE_CANCEL`：取消数（默认 2，范围 1..6，需小于 waiter 数）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_semaphore_release_wake/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=11 TORTURE_ROUNDS=8 TORTURE_WAITERS=24 TORTURE_CAPACITY=5 GOGC=1 GOMAXPROCS=2 /tmp/case
```
