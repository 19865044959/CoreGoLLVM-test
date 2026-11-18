---
id: l02_ctx_timeout_prodcons
dim: L02_concurrency
priority: P1
sources:
  - fasthttp@v1.59.0: server_test.go:2914 / 2981 超时处理器返回 408 后原 handler 继续运行，不得写已复用 ctx
  - fasthttp@v1.59.0: client_test.go:2827 / 2906 连接等待超时路径返回错误且等待队列无残留
  - etcd@v3.5.0: server/etcdserver/api/rafthttp/pipeline_test.go:71 缓冲满时非阻塞发送，stop 后 select 立即返回
tags: [L02, context, WithCancel, WithDeadline, DeadlineExceeded, producer-consumer, residual-drain, gc]
requires: []
runtime: "<3s"
---

# 场景

模拟 fasthttp 超时 handler 与 etcd pipeline 的取消路径，所有判定都不依赖时长：

1. 取消消费：48 条指针消息预填入 cap=48 的 channel 后 close；消费者只处理前 16 条，到阈值后阻塞等待 `ctx.Done()`。主 goroutine 收到 reached 信号后显式 `cancel()`，消费者校验取消原因与持有 payload；主 goroutine 再用普通 receive 排空剩余 32 条，验证取消后残留可确定性回收；
2. 截止时间已过：用 `context.WithDeadline(..., time.Unix(0,0))` 构造立即到期的 context，生产者对 24 条消息逐次 `select { case ch <- msg: ...; case <-ctx.Done(): ... }`，消费者只等待 Done；两边都必须走 Done 分支，发送数为 0、放弃数为 24。

各阶段之间与排空过程中显式分配垃圾并 `runtime.GC()`，对持有的 `*packet` 再校验。

# 期望行为

1. 取消前消费者恰好处理 16 条且顺序正确，取消原因为 `context.Canceled`；
2. 取消后剩余 32 条全部被排空、无重复无丢失，48 条校验和正确；
3. 已到期 context 立即报 `DeadlineExceeded` 且 Done 已关闭；
4. 到期场景中阻塞发送 24 次全部放弃、消费者确定观察到 Done；
5. 最后一行 `ALL PASS l02_ctx_timeout_prodcons`。

# 失败判读

- `context canceled before the consumption prefix` / `consumer did not observe`：取消协调或 context 传播错误；
- `residual` / `packets not accounted for`：取消后残留消息丢失或重复；
- `expired producer sent` / `aborts`：无接收者时发送仍能成功，或 Done 分支未被选中；
- `packet corrupted`：消息指针在 GC 后失效。

# 参数

- `UAF_SEED`：决定 packet tag 派生值，失败输出带种子；
- 无 TORTURE 参数；无 sleep/墙钟等待，到期路径使用过去时刻的 deadline。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_ctx_timeout_prodcons/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
