---
id: l02_actor_mailbox_state
dim: L02_concurrency
priority: P1
sources:
  - etcd@v3.5.0: raft/node_test.go:353 node.run 单 goroutine 事件循环，select{tick, prop, recv, stop} 推进状态，关闭 stop 后退出
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37 100 个 goroutine 调度、单消费者串行执行，执行序等于调度序
  - nats-server@v2.11.0: server/ipqueue_test.go:253 3 个生产者并发 push、单消费者批量 pop，通知 channel 不丢唤醒
tags: [L02, actor, mailbox, channel, single-owner, fifo-command, close-drain, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 etcd raft node 的单 goroutine 事件循环与 grpc-go callback serializer 的单消费者串行执行：actor 独占 `map[int]*record` 状态，外部只能通过 cap=8 的 mailbox 发送命令。

- 4 个生产者各自向自己的 key 顺序发送 50 条 `incr` 命令，每条携带 `seq` 与 `*record` payload；
- 2 个读者各发 30 条 `get` 命令并等待 reply，校验值单调不减且不越界；
- actor 在单 goroutine 内按 mailbox FIFO 处理，按 key 校验 `seq` 严格递增，任何乱序立即失败；
- 每 25 条命令 actor 分配垃圾并 `runtime.GC()`，随后遍历状态校验每个 record tag；
- 生产者与读者全部结束后，主 goroutine 发送 snapshot 命令，再关闭 mailbox；actor 排空缓冲后交接最终状态并退出。

# 期望行为

1. 每个生产者的 50 条命令按 key 的 seq 顺序被 actor 观察，无乱序；
2. 4 个 key 的最终值都恰为 50，`ops==200`；
3. 2 个读者共 60 次 get 返回的值单调不减且落在 `[0,50]`；
4. snapshot 返回 key 数 4；
5. mailbox 关闭后 actor 处理完所有已入队命令，`done` 关闭，最终状态通过 channel 交接；
6. 最终 record 的 tag/val 在多轮显式 GC 后保持不变；
7. 最后一行输出 `ALL PASS l02_actor_mailbox_state`，退出码 0。

# 失败判读

- `actor observed out-of-order command`：mailbox FIFO 或生产者发送顺序语义被破坏（同一生产者内）；
- `key N val=M want=50`：命令丢失或重复执行；
- `actor value regressed`：读请求看到回退状态，actor 不是单写者；
- `actor state corrupted by GC`：actor 持有的指针跨 GC 失效；
- 超时：mailbox 满且 actor 未推进，或读者等待 reply 的唤醒链断裂。

# 参数

- `UAF_SEED`：决定 record payload 的种子值；
- 无 `TORTURE_*`；迭代固定。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_actor_mailbox_state/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1,asyncpreemptoff=1 /tmp/case
```
