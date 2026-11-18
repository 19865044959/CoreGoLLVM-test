---
id: l02_pubsub_broker_drop
dim: L02_concurrency
priority: P2
sources:
  - grpc-go@v1.71.1: internal/grpcsync/pubsub_test.go:58 两订阅者按序收到 10 条消息；订阅前发布的消息只回放最后一条；cancel 后 Done 关闭且不再回调
  - etcd@v3.5.0: server/mvcc/watchable_store_test.go:532 256 个 watch stream、缓冲仅 1，慢订阅者触发淘汰与通知
  - nats-server@v2.11.0: server/sublist_test.go:1127 兴趣通知采用非阻塞 send 合并事件
tags: [L02, pubsub, broker, slow-consumer, nonblocking-drop, replay, unsubscribe-close, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认发布 20 条；TORTURE_ROUNDS 放大消息数，上限 20 倍）"
---

# 场景

模拟 grpc-go `grpcsync/pubsub` 的订阅/回放/取消语义，并叠加 nats-server 慢消费者非阻塞丢弃：

- broker 在 `Mutex` 下维护订阅者列表与最后一条消息；
- `Subscribe` 时若已有 last message，则将其回放进新订阅者缓冲；
- `Publish` 在锁内对每个 active 订阅者做 `select { case ch <- m: default: dropped++ }`，慢订阅者不阻塞发布者；
- `Unsubscribe` 在锁内标记 inactive 并 `close(ch)`，与 `Publish` 互斥，避免 send-on-closed。

默认场景：发布 1 条 `pre` 后订阅 3 个 fast（缓冲足够大）与 1 个 slow（缓冲 3）。3 个 fast 消费者 goroutine 校验每条消息序号严格递增；slow 不消费，发布 20 条后其缓冲保留前 3 条、丢弃 17 条。随后排空 slow、退订全部、验证关闭后再发布不 panic。另做一次并发发布 + 退订的竞态阶段。

# 期望行为

1. 订阅前发布的 `pre` 只回放最后一条，四个订阅者各收到 1 条；
2. 3 个 fast 订阅者各按 `1..total` 顺序收到全部消息，无遗漏/重复/乱序；
3. slow 订阅者保留序号 1、2、3 三条，丢弃计数恰为 `total-3`；
4. 退订后订阅 channel 关闭，排空后接收返回 `ok=false`；
5. 全部退订后再发布不 panic、不产生丢弃计数；
6. 并发发布与退订阶段不 panic、不死锁，channel 最终关闭；
7. 消息指针在多次显式 GC 后内容不变；
8. 最后一行输出 `ALL PASS l02_pubsub_broker_drop`，退出码 0。

# 失败判读

- `slow drops=N want=...`：非阻塞丢弃或慢消费者缓冲策略错误；
- `fast subscriber received wrong message`：发布/订阅路径乱序或丢消息；
- `channel not closed after unsubscribe`：退订未关闭 channel；
- `publish after unsubscribe must not drop`：inactive 订阅者仍在投递列表中；
- panic `send on closed channel`：发布与退订未正确互斥，属严重竞态；
- 超时：慢消费者阻塞了发布者，违反非阻塞投递约束。

# 参数

- `UAF_SEED`：决定消息数值；
- `TORTURE_ROUNDS`：放大发布消息数（默认 1 → 20 条；上限 20 → 400 条）；fast 缓冲随 total 扩到 `total+2`，slow 固定为 3。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_pubsub_broker_drop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
