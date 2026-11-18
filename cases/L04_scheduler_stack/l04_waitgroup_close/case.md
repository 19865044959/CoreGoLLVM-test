---
id: l04_waitgroup_close
dim: L04_scheduler_stack
priority: P2
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:105 100 个并发回调各自 wg.Done，全部执行且不丢唤醒
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:125 Close 后已入队元素仍可读出，重复 Close 幂等
  - pion/webrtc@v4.1.2: operations_test.go:55 GracefulClose 后不执行新入队 operation，关闭前队列保留
tags: [L04, WaitGroup, close, producer-consumer, channel, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_ITEMS 可放大）"
---

# 场景

模拟 gRPC 队列关闭与 WebRTC operations GracefulClose 的 WaitGroup+close 模式：每轮 6 个生产者向容量 8 的 `jobs` channel 发送两节点任务，`prodWG.Wait()` 后由独立 goroutine `close(jobs)`；4 个消费者 `range jobs` 排空。消费者在排空到总任务数一半时全部停在 release 上，主协程此时显式 GC 两次（任务指针在 channel 缓冲区、消费者栈和主协程 `all` 表中），再放行。默认 2 轮，每轮 288 个任务；关闭完成后再次 GC 并校验全部任务。

# 期望行为

1. 每轮恰好处理 `producers×perProd` 个任务，id 无缺失、无重复；
2. 所有任务链的两节点差值为 5，内容在关闭+GC 后不变；
3. 每轮校验和等于独立公式值；
4. 消费者在中点阻塞时 GC 不破坏栈上/队列中的指针；
5. closer 在 `prodWG.Wait()` 之后才关闭 channel，消费者正常退出；
6. 最后一行输出 `ALL PASS l04_waitgroup_close`。

# 失败判读

- `item ... missing after close+GC`：关闭路径丢任务或指针被提前回收；
- `chain corrupted`：任务链内容被破坏；
- `sum=...`：任务重复/遗漏或值错误；
- `halfDone` 等待超时：消费者未排空到一半，可能任务总数不足或生产者阻塞；
- 超时：WaitGroup/close 顺序错误（例如消费者 blocked 而生产者在满 channel 上等待）。

# 参数

- `UAF_SEED`：任务基值与轮次偏移种子（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–20；
- `TORTURE_ITEMS`：每个生产者每轮任务数，默认 48，范围 4–512。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_waitgroup_close/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=5 TORTURE_ITEMS=128 GOGC=1 GOMAXPROCS=2 /tmp/case
```
