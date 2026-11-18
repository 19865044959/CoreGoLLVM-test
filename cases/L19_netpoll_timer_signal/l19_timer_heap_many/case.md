---
id: l19_timer_heap_many
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - nats-server@v2.11.0: server/thw/thw_test.go:22 时间轮批量插入/到期/回收，timer 堆中保存大量待触发任务
  - etcd@v3.5.0: server/lease/lease_queue_test.go:22 最小堆按到期时间组织 50 个租约并在到期后回填
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 多次 Reset 的 timer 仍需按时触发
tags: [L19, timer-heap, afterfunc, closure, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<5s（默认 1 轮 2048 个 timer；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟 NATS 时间轮与 etcd 租约最小堆的批量定时任务：每轮创建 2048 个 `time.AfterFunc`，每个闭包捕获一条独立的三节点指针链（base、base+1、base+2）。timer 刚进入堆、绝大多数尚未到期时，主协程分配垃圾并显式执行两轮 `runtime.GC()`；到期回调先在 gate 上阻塞，使闭包同时被 timer heap、闭包对象和回调 goroutine 栈引用。放行后所有回调校验指针链内容并汇总校验和。

# 期望行为

1. 每个 timer 恰好触发一次，`fired == 2048`；
2. 每个回调读到的三节点指针链内容完整，`bad == 0`；
3. 所有 payload 校验和等于独立计算的 `Σ (base*3+3)`；
4. 默认 1 轮通过；`TORTURE_ROUNDS` 放大轮数后每轮仍全部通过；
5. 最后一行输出 `ALL PASS l19_timer_heap_many`。

# 失败判读

- `fired=... want=...`：timer 丢失或重复触发，timer heap 链接/调度异常；
- `timer payload chains corrupted`：闭包捕获的指针在 GC 后失效，典型 timer heap 扫描或写屏障问题；
- `sum=... want=...`：节点内容/数量损坏；
- 超时：回调未执行或 gate 协调死锁。

# 参数

- `UAF_SEED`：每轮 base 的种子偏移（默认 1）；
- `TORTURE_ROUNDS`：轮数，默认 1，范围 1–20；每轮 2048 个 timer，固定 1ms 延时，不做耗时阈值断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_heap_many/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=5 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
