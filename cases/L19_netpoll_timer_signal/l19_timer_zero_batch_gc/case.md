---
id: l19_timer_zero_batch_gc
dim: L19_netpoll_timer_signal
priority: P2
sources:
  - quic-go@v0.50.1: internal/utils/timer_test.go:88 TestTimerZeroDeadline 零 deadline timer 的立即到期语义
  - nats-server@v2.11.0: server/events_test.go:3173 连接计数归零时停止周期 timer，停止后不得再回调
  - grpc-go@v1.71.1: internal/idle/idle_test.go:133 空闲超时 timer 到期后回调置状态并通知
tags: [L19, afterfunc, zero-duration, timer-heap, batch, stop-after-fire, pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 2 轮 × 256 个零延时 timer；TORTURE_ROUNDS 放大轮数）"
---

# 场景

模拟大量连接/租约同时到期的边界：每轮挂入 256 个 `AfterFunc(0)`，回调先向
`entered` 报到，再阻塞在共享 gate 上。主协程收齐 256 次报到后，此时全部 timer
已到期且 256 个回调栈同时存在；对前 32 个 timer 调 `Stop` 必须全部返回 false。
主协程显式 GC 校验全部指针链后放行 gate，每个回调恰好执行一次。最后用哨兵
零 timer 再 GC，确认没有迟到/重复回调。`TORTURE_ROUNDS` 放大轮数。

# 期望行为

1. 256 个零延时回调全部进入阻塞态，主协程收齐报到后 GC 不崩溃；
2. 对已开始回调的 32 个 timer，`Stop` 全部返回 false；
3. GC 后 256 条闭包指针链内容完整；
4. 放行后 `fired` 恰好 256，无重复回调；
5. 哨兵 timer 触发并再次 GC 后计数不变、duplicates 为 0；
6. 默认 2 轮通过；`TORTURE_ROUNDS` 放大后每轮均通过，末行
   `ALL PASS l19_timer_zero_batch_gc`。

# 失败判读

- `Stop on expired callback timer ... returned true`：timer 到期状态判断错误；
- `payload corrupted`：timer heap 或阻塞回调栈上的指针在 GC 后失效；
- `fired=... want=256`：回调丢失或重复触发；
- `duplicate callbacks=...`：timer 堆链接/摘除异常导致重复调度；
- 超时：256 个回调未全部启动或 gate 协调死锁。

# 参数

- `UAF_SEED`：每轮 payload 基址种子；
- `TORTURE_ROUNDS`：轮数，默认 2，范围 1–20；每轮固定 256 个零延时 timer；
- 所有等待使用 10s 上限，仅防死锁，不做耗时阈值断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_zero_batch_gc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=10 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
