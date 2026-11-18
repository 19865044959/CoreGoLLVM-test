---
id: l02_lock_chan_hybrid
dim: L02_concurrency
priority: P1
sources:
  - nats-server@v2.11.0: server/ipqueue_test.go:253 三生产者并发 push、单消费者批量 pop，通知 channel 不丢唤醒
  - grpc-go@v1.71.1: internal/buffer/unbounded_test.go:90 10 写 1 读无界队列的完整性与收敛
  - gorm@v1.30.0: tests/prepared_stmt_test.go:153 MaxOpenConns(1) 下 100 goroutine 并发 Create/First，缓存 + 单连接信号量
tags: [L02, mutex, channel-notify, lost-wakeup, fifo, coalesced-wakeup, close-drain, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟“互斥锁保护数据 + 容量 1 channel 做边沿唤醒”的混合队列：锁保护 FIFO 缓冲与 `order` 分配，channel 通知允许合并，消费者每次唤醒后必须重新检查条件。对应 nats-server ipqueue 的“切片 + 通知 channel”以及 grpc buffer 的多写单读模式。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_lock_chan_hybrid`；
2. 空队列上已取消的 Pop 立即返回 `context.Canceled`；Close 幂等，关闭后 Push 拒绝、残留项可排空、排空后 Pop 返回 closed；
3. 4 个消费者阻塞在空队列时，4 条边沿通知即使被合并也不会丢条目，全部消费；
4. 4 生产者 × 30 条并发写入、3 消费者消费：不重不漏，按锁内 `order` 还原后每条生产者 FIFO 保持；
5. 载荷在 GC 前后与最终校验一致。

## 失败判读

- `coalesced notification lost an item`：消费者未在唤醒后循环复查条件；
- `producer FIFO broken`：`order` 未在锁内分配或队列实现不是严格 FIFO；
- `push after close` 相关失败：关闭标志与 done channel 的发布顺序错误；
- 超时/死锁：Close 未关闭 done，或 Pop 在 closed 后仍等待通知。

## 参数

- `UAF_SEED`：决定载荷数值，失败信息带种子；
- 固定 4 消费者/4 生产者 × 30 条，无墙钟阈值。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_lock_chan_hybrid/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
