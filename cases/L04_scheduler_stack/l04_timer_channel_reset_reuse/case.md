---
id: l04_timer_channel_reset_reuse
dim: L04_scheduler_stack
priority: P1
sources:
  - go1.24.1: src/time/sleep_test.go:552 TestReset 覆盖 Timer.Reset 后的旧代事件/通道语义
  - go1.24.1: src/time/sleep_test.go:355 TestAfterStop 覆盖 Stop 后 timer.C 不再产生事件
tags: [L04, timer, Timer.Reset, Stop, channel-drain, select, timer-heap, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 NATS 时间轮与 gRPC idle timer 的“定时器通道 + 业务 channel”组合：单个服务 goroutine 用 `select` 同时读 `time.Timer.C` 与 jobs channel。每轮先显式 GC，再 `Reset(1ms)` 并等待恰好一个 tick，tick 必须携带服务 goroutine 持有的 3 节点链校验和；每轮结束断言 timer 通道无重复 tick。随后把 timer 停掉换 1h 并 `Stop`，通过向 jobs 发送哨兵 job 作为屏障：屏障前的 job 事件必须全部转发、tick 事件必须为 0，屏障后无残留事件。

# 期望行为

1. 每轮 Reset 后恰好收到 1 个 timer tick，且值等于持有链的校验和；
2. 每轮结束后 `len(timer.C)==0`，无同代重复事件；
3. 已到期 timer 的 `Stop` 返回 false，排空后通道为空；
4. `Reset(1h)` 后 `Stop` 返回 true，最终通道为空；
5. 停止 timer 后，10~13 个 job 全部转发且值集合完全匹配，tick 计数为 0；
6. 哨兵屏障后的带缓冲事件排空后无任何残留；
7. 最后一行输出 `ALL PASS l04_timer_channel_reset_reuse`。

# 失败判读

- tick 计数非 0 或残留事件：Stop/Reset 未清理旧代 timer，或通道排空语义错误；
- tick 校验和不匹配：timer 回调 goroutine 与持有链之间的栈/闭包指针问题；
- job 缺失/重复：select 服务循环在 timer 与 jobs 间丢事件；
- `len(t.C)!=0`：停止/排空语义破坏；
- 超时：timer 未触发或服务 goroutine 卡在 select。

# 参数

- `UAF_SEED`：轮数与链/任务值（轮数 16 + seed%5，默认 seed=1 时 17 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_timer_channel_reset_reuse/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=4 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
