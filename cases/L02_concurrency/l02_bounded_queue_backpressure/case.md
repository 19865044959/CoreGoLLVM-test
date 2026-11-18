---
id: l02_bounded_queue_backpressure
dim: L02_concurrency
priority: P2
sources:
  - nats-server@v2.11.0: server/norace_test.go:62 1MB × 500 慢消费者背压
  - quic-go@v0.50.1: connection_test.go:898 未处理包上限与有界缓冲丢弃语义
  - tailscale@v1.80.2: net/memnet/pipe_test.go:83 容量 1 管道的写阻塞与逐字节读
tags: [L02, bounded-queue, backpressure, sync.Cond, producer-consumer, gc, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 12 项；TORTURE_ROUNDS 放大队列项数）"
---

# 场景

模拟 nats-server 慢消费者、quic-go 有界缓冲与 tailscale memnet 容量管道的背压：自实现 cap=4 的环形队列（`sync.Mutex + sync.Cond`）。生产者先填满队列并在第 5 次 push 时进入 `cond.Wait`，同时通过 `blockedCh` 发出“已阻塞”信号；消费者的排空入口由 `gate` channel 控制，主 goroutine 收到阻塞信号后才放行消费者，因此“生产者确实被背压阻塞”是可判定的，而非靠 sleep 或墙钟。消费者每 4 项显式 GC 并校验刚弹出的指针 payload，生产者也周期性 GC。

# 期望行为

1. 生产者至少发生一次阻塞（`blocked >= 1`），最大队列深度恰好等于 cap=4；
2. 消费者按 FIFO 顺序恰好消费全部项，无重复、无丢失，校验和正确；
3. 生产者持有与消费者弹出的 payload 指针跨 GC 内容不变；
4. 生产结束 close 后消费者能正常排空并退出，无死锁；
5. 默认输出 `capacity=4 items=12 max_depth=4`，最后一行 `ALL PASS l02_bounded_queue_backpressure`。

# 失败判读

- `producer finished without ever blocking`：队列未真正限容，背压语义缺失；
- `max queue depth`：队列超过容量（覆盖或 ring 索引错误）；
- `consumer observed invalid or out-of-order item`：FIFO/环形索引或关闭排空错误；
- `consumed item corrupted after GC`：弹出项指针被误回收；
- 超时：Cond 丢失唤醒或 gate 协调死锁。

# 参数

- `TORTURE_ROUNDS`：队列项数倍数（默认 12 项，默认 1 轮），范围 1..200；
- `UAF_SEED`：由 runner 注入但本用例不使用。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_bounded_queue_backpressure/*.go
GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
