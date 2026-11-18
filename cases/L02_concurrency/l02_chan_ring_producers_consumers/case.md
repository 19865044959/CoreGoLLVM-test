---
id: l02_chan_ring_producers_consumers
dim: L02_concurrency
priority: P2
sources:
  - nats-server@v2.11.0: server/ipqueue_test.go:253 三生产者/一消费者 + 通知 channel + 切片池
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:90 10 写 1 读无界队列的完整性校验
tags: [L02, producer-consumer, bounded-channel, pointer-message, sync.WaitGroup, gc, check-sum, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 480 条消息；TORTURE_ROUNDS 放大每生产者条数）"
---

# 场景

模拟 nats-server `ipqueue` / grpc-go `unbounded` 的生产者-消费者队列模型，但换成有界 channel 制造真实并发：4 个生产者经 cap=16 的 `chan message`（消息持有 `*payload`）向 3 个消费者投递固定 480 条指针消息。每个生产者在发送前、每个消费者在处理 32 条后显式分配垃圾并 `runtime.GC()`，让指针消息跨 GC 存活。消费端用按 `prod*perProd+seq` 索引的 atomic 位图去重，并累加固定种子派生的校验和。

# 期望行为

1. 480 条消息全部被消费，`total == 4*120`，无重复、无丢失（位图全 1）；
2. 每条 payload 的 `prod/seq` 在范围内，聚合校验和等于主 goroutine 独立计算值；
3. 发送前与接收后各做显式 GC + 垃圾分配，payload 指针内容不被破坏；
4. 生产者全部结束后才 close(channel)，消费者 range 正常退出，无 panic/死锁；
5. 输出仅含固定计数，最后一行 `ALL PASS l02_chan_ring_producers_consumers`。

# 失败判读

- `duplicate message` / `missing message`：channel 关闭与排空时序错误或消息丢失/重复；
- `payload corrupted after GC` / `producer payload corrupted before send`：指针消息在 GC 后失效，对老→新引用或栈位图漏标要高度怀疑；
- `message count` / `aggregate checksum mismatch`：消息内容或计数被破坏（种子在失败行输出）；
- 超时：生产/消费互相等待（channel 容量或 close 时序死锁）。

# 参数

- `UAF_SEED`：生成校验和所用 tag 的种子（同一 seed 结果确定，失败行打印 seed）；
- `TORTURE_ROUNDS`：每生产者消息数倍数，默认 1，范围 1..50；失败输出带 seed。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_chan_ring_producers_consumers/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=1 TORTURE_ROUNDS=5 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
