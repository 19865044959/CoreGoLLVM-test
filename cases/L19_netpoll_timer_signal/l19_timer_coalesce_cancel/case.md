---
id: l19_timer_coalesce_cancel
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - nats-server@v2.11.0: server/sublist_test.go:1127 订阅兴趣变化用非阻塞通知合并多次事件
  - quic-go@v0.50.1: connection_test.go:1749 发送队列阻塞时反复重置 pacing timer，避免重复唤醒
  - etcd@v3.5.0: server/lease/lease_queue_test.go:22 批量租约 timer 的插入与取消
tags: [L19, timer-reset, coalescing, batch-cancel, afterfunc, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<2s（默认 1 轮；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟 NATS 订阅兴趣通知合并与 quic-go pacing timer 的上游路径：每轮先让 1024 个逻辑活动事件反复 `Reset` 同一个 `time.AfterFunc` timer（所有事件合并为一次物理触发），再创建 2048 个 1 小时的 `AfterFunc` timer 并由 4 个 goroutine 批量 `Stop`。两个阶段都在 timer 仍在堆中时显式 `runtime.GC()` 并分配垃圾，关闭的回调捕获三节点指针链。

# 期望行为

1. 合并阶段 1024 次 Reset 后回调恰好触发 1 次；
2. 回调触发前后 GC，1024 条 payload 指针链全部完整；
3. 批量取消阶段 2048 个 Stop 全部返回 true，重复 Stop 返回 false；
4. 哨兵 timer 触发并再次 GC 后，被取消的 timer 回调次数为 0、payload 未损坏；
5. 默认 1 轮通过；`TORTURE_ROUNDS` 放大轮数后每轮均通过；
6. 最后一行输出 `ALL PASS l19_timer_coalesce_cancel`。

# 失败判读

- `coalesced fires=...`：Reset 合并语义异常，出现重复或丢失回调；
- `coalesced payloads corrupted`：timer 闭包捕获的指针在 GC 后失效；
- `batch cancel stopped=...`：批量 Stop 未能全部取消；
- `canceled timers fired=...`：已 Stop 的 timer 仍触发，timer 堆摘除异常；
- 超时：哨兵 timer 未送达或回调协调死锁。

# 参数

- `UAF_SEED`：payload base 种子（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 1，范围 1–20；每轮固定 1024 个合并事件与 2048 个取消 timer；
- 合并 timer 固定 25ms、取消 timer 固定 1 小时；所有等待使用 10s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_coalesce_cancel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=3 TORTURE_ROUNDS=10 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
