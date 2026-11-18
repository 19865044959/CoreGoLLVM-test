---
id: l02_bounded_queue_cancel
dim: L02_concurrency
priority: P2
sources:
  - etcd@v3.5.0: server/etcdserver/api/rafthttp/pipeline_test.go:71 阻塞发送下消息缓冲满后第 N+1 个发送被拒绝；stop 后 select 立即返回
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 并发等待者被 context 取消后从等待集合移除
  - tailscale@v1.80.2: logtail/logtail_test.go:24 已取消 ctx 上 Shutdown 立即返回，不阻塞
tags: [L02, bounded-queue, context-cancel, close-wakeup, drain, blocked-producer, blocked-consumer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 2 轮；TORTURE_ROUNDS 放大轮数，上限 50）"
---

# 场景

模拟 etcd rafthttp pipeline 与 quic-go 流等待者的“有界队列 + 取消”语义。队列容量 4，基于 buffered channel + `done` channel + `atomic.Bool closed` 实现：

- `Push(ctx, it)`：满队列阻塞时由自己的 `ctx` 取消或统一 `Close` 唤醒，返回 `context.Canceled` / `errQueueClosed`；
- `Pop(ctx)`：空队列阻塞时同样可被取消；关闭后先排空残留项，再返回 `errQueueClosed`；
- `Close` 用 `sync.Once` 幂等，关闭后新 `Push` 必须被确定性拒绝。

每轮四个阶段，全部用“启动计数 + 有界 Gosched 自旋”同步，无 sleep/墙钟：

1. 预填满队列，3 个生产者阻塞后逐个取消各自 ctx；
2. 空队列上 3 个消费者阻塞后取消各自 ctx；
3. 预填满队列，3 个生产者阻塞后 `Close` 唤醒；随后排空 4 条残留并校验 FIFO，关闭后 Push 被拒绝；
4. 空队列上 3 个消费者阻塞后 `Close` 唤醒。

每个同步点与每条排空项之间显式分配垃圾并 `runtime.GC()`。

# 期望行为

1. 满队列上阻塞的 3 个生产者全部以 `context.Canceled` 返回，无一成功入队；
2. 空队列上阻塞的 3 个消费者全部以 `context.Canceled` 返回；
3. `Close` 后阻塞的生产者全部以 `errQueueClosed` 返回，且不会被误判为成功；
4. `Close` 后阻塞的消费者全部以 `errQueueClosed` 返回；
5. 已关闭队列仍能按 FIFO 排空 4 条残留项，排空后 `Pop` 返回 `errQueueClosed`；
6. 关闭后 `Push` 返回 `errQueueClosed`，不 panic；
7. 每轮 4 条残留项在 GC 后 payload 不变；
8. 多轮（默认 2 轮）聚合计数与轮数严格成比例；
9. 最后一行输出 `ALL PASS l02_bounded_queue_cancel`，退出码 0。

# 失败判读

- `producers did not start` 或 `waitFor` 达上限：goroutine 未调度或队列状态检查死锁；
- `blocked producer error=... want canceled`：满队列 `Push` 未响应 ctx；
- `blocked consumer error=... want canceled`：空队列 `Pop` 未响应 ctx；
- `closed producer/consumer error=... want closed`：`Close` 未广播或 `done` 未关闭；
- `drained item corrupted`：关闭排空顺序/内容错误，或残留项被提前丢弃；
- `push after close must be rejected`：关闭后仍接受新元素；
- 超时：等待中的 goroutine 未被取消/关闭唤醒。

# 参数

- `UAF_SEED`：决定队列项 payload 数值；
- `TORTURE_ROUNDS`：放大轮数（默认 2，上限 50），每轮独立创建 4 个队列，聚合计数随之线性增长。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_bounded_queue_cancel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=50 GODEBUG=clobberfree=1 /tmp/case
```
