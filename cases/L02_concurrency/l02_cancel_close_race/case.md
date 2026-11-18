---
id: l02_cancel_close_race
dim: L02_concurrency
priority: P2
sources:
  - grpc-go@v1.71.1: test/goaway_test.go:415 客户端 Close 与服务端 GOAWAY 并发，sync.Once 广播，不得 panic/死锁
  - etcd@v3.5.0: raft/node_test.go:525 事件循环 Stop 幂等与 done 关闭
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 并发等待者被 context 取消后从等待集合移除
tags: [L02, context-cancel, channel-close, close-race, terminal-outcome, no-send-on-closed, accounting, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 6 轮；TORTURE_ROUNDS 放大轮数，上限 50）"
---

# 场景

模拟服务优雅退出时 Close 与共享 context Cancel 同时发生：多个发送者阻塞在满队列（或正在争抢空位），两个唤醒源在同一时刻起跑。每个发送者必须恰好以 `closed` 或 `context.Canceled` 之一退出，不得 panic、双重唤醒或把失败条目留在队列里。对应 grpc Close/GOAWAY、etcd Stop 幂等和 quic-go 取消等待者清理。

## 期望行为

1. 进程退出码为 0，最后一行 `ALL PASS l02_cancel_close_race`；
2. 每轮阶段 A（满队列）：8 个发送者全部失败，终态只能是 closed/canceled，排空结果恰为预填的 4 条；
3. 每轮阶段 B（空队列、允许成功）：`sent + closed + canceled == 8`，且成功条数 ≤ 容量；排空条数等于成功条数，成功条目与发送者本人报告一一对应，无重复；
4. 两个阶段在 Close/Cancel 后再次发送都必须返回 closed；无 panic；
5. 载荷跨 GC 完整。

## 失败判读

- `sender panicked`：向已关闭 channel 发送或重复关闭；
- `terminal outcome count mismatch`：某个发送者返回了第三种错误或在 select 中丢失唤醒；
- `item in queue but sender reported failure`：成功入队与结果记录之间存在竞态；
- `drained > sent`：失败条目录入了队列，需检查发送路径。

## 参数

- `UAF_SEED`：载荷种子，失败信息带种子；
- `TORTURE_ROUNDS`：放大轮数（默认 6，上限 50），每轮固定 8 发送者 + 容量 4；
- 不使用时间阈值，Close/Cancel 通过同一 gate 同时起跑。

## 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_cancel_close_race/*.go
UAF_SEED=1 TORTURE_ROUNDS=20 GODEBUG=clobberfree=1 /tmp/case
```
