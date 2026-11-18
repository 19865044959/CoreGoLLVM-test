---
id: l02_backpressure_deadline
dim: L02_concurrency
priority: P1
sources:
  - etcd@v3.5.0: server/etcdserver/api/rafthttp/pipeline_test.go:71 阻塞发送下消息缓冲满后第 N+1 个发送必须被拒绝/超时；stop 后 select 立即返回
  - quic-go@v0.50.1: connection_test.go:898 未处理包上限与有界缓冲的丢弃语义
  - fasthttp@v1.59.0: client_test.go:2827 / 2906 连接等待超时路径返回错误且等待队列无残留
tags: [L02, backpressure, bounded-queue, context-deadline, DeadlineExceeded, deterministic-expiry, accounting, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟有界队列的背压与超时边界：队列满时，带已过期 deadline 的 Push 必须确定性失败；带活跃 context 的阻塞 Push 在消费者释放一个槽位后恰好成功一次，其余被取消；空队列上带过期 deadline 的 Pop 必须立即返回。对应 etcd rafthttp 满缓冲拒绝、quic-go 有界缓冲以及 fasthttp 连接等待超时。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_backpressure_deadline`；
2. 满队列 + 过期 deadline：3 个 Push 全部 `DeadlineExceeded`，队列占用不变；
3. 满队列 + 活跃 ctx：释放 1 个槽位后恰有 1 个 Push 成功、其余 2 个 `Canceled`，占用回到容量；
4. 空队列 Pop 过期 deadline 立即 `DeadlineExceeded`；活跃 Pop 收到协调 Push 的条目；
5. 满队列 + Cancel 竞态：4 个 Push 全部取消，无条目入队；
6. 收尾满足 `enqueued == drained + popped`，所有条目唯一且载荷正确。

## 失败判读

- `full-queue expired push error=nil`：发送路径在 ctx 已过期时仍写入；
- `accepted after relief != 1`：释放一个槽位唤醒了多个发送者，或容量记账错误；
- `empty-queue expired pop error=...`：Pop 未优先检查 ctx；
- `accounting drained=...`：取消路径遗留条目或重复计数；
- 超时：等待活跃发送者的条件未被满足（消费者未释放槽位）。

## 参数

- `UAF_SEED`：载荷种子，失败信息带种子；
- 不使用真实时延：过期 deadline 用 `time.Unix(0,0)` 构造，活跃 deadline 为 1 小时后且测试内取消；所有计数固定。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_backpressure_deadline/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
