---
id: l19_timer_many_waiters
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 100 个并发等待者中随机取消一部分，其余按限流放行
  - etcd@v3.5.0: server/lease/lease_queue_test.go:22 50 个租约 timer 按到期时间批量组织与取消
  - nats-server@v2.11.0: server/thw/thw_test.go:122 时间轮批量插入大量到期任务并统一回收
tags: [L19, timer-heap, concurrent-waiters, stop, select, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 512 个并发等待者；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟大量连接/流同时挂起超时 timer 的路径：512 个 goroutine 各自创建独立
`time.Timer`，其中 1/4 在等待前 `Stop()` 并关闭自己的 cancel channel，其余
在 timer 到期后放行。每个等待者栈上持有一条 3 节点指针链；所有 timer 入堆后
主协程显式 GC，再逐个核对“到期 / 取消”集合与 payload。

## 期望行为

1. 512 个等待者全部就位后 GC 不崩溃，timer heap 中的活跃 timer 正常到期；
2. 到期数 = 384，取消数 = 128，二者不互相串扰；
3. 取消路径的 timer 不得投递 tick；
4. 每个等待者的指针链在 GC 前后内容一致，校验和正确；
5. 所有等待 goroutine 在超时保护内退出；
6. 最后一行输出 `ALL PASS l19_timer_many_waiters`，失败退出码为 1。

## 失败判读

- `fired`/`canceled` 计数偏差：`Timer.Stop` 语义错误或 timer 堆索引损坏导致误触发；
- `FAIL: ... payload ... corrupted`：等待者栈上的指针未被栈扫描保活；
- 超时 10s：timer channel 丢唤醒或等待 goroutine 泄漏；
- 崩溃：timer 堆在大量并发插入/删除时内部结构被破坏。

## 参数

- `UAF_SEED`：随机路径种子，参与 payload 基值计算；
- `TORTURE_ROUNDS`：轮数（1–10），每轮重建 512 个 timer 与等待者。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_many_waiters/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
