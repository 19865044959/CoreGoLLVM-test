---
id: l04_timer_storm_deadline
dim: L04_scheduler_stack
priority: P2
sources:
  - grpc-go@v1.71.1: internal/idle/idle_test.go:133 空闲超时 timer 触发后 manager 进入 idle
  - tailscale@v1.80.2: wgengine/watchdog_test.go:15 连续操作后 Close，watchdog timer 不得误报
tags: [L04, timer, AfterFunc, timer-heap, closure-pointer, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_TIMERS 可放大）"
---

# 场景

模拟 gRPC idle timer 与 tailscale watchdog 的定时器集中到期：每轮以同一 1ms 截止时间批量挂载 `TORTURE_TIMERS`（默认 128）个 `time.AfterFunc`，每个闭包捕获一条独立的 3 节点堆指针链。全部 timer 仍在 timer heap 上时显式 GC；回调执行到一半时再次 GC，覆盖 timer heap、闭包对象和回调 goroutine 栈三种持有位置。主协程逐个接收回调完成信号，不按耗时判定。

# 期望行为

1. 每个 timer 恰好触发一次（`fired == 1`），总触发数等于挂载数；
2. 全部回调中捕获链的 id/值关系 `b, b+1, b+2` 不变；
3. 回调汇总校验和等于独立公式 `Σ(3b+3)`；
4. 全部触发并再次 GC 后，记录中保存的链仍完整；
5. 进程退出码为 0，最后一行输出 `ALL PASS l04_timer_storm_deadline`。

# 失败判读

- 等待回调时卡住：timer 未触发或闭包被错误回收，检查 timer heap 的指针标记；
- `fired=... want=1`：timer 重复触发或漏触发；
- `chain corrupted`：闭包捕获链在 GC 后损坏，优先怀疑闭包对象/栈槽位图；
- 超时：timer 风暴中某回调阻塞或 GC 无法完成。

# 参数

- `UAF_SEED`：链值种子；
- `TORTURE_ROUNDS`：轮数，默认 2（1–8）；
- `TORTURE_TIMERS`：每轮 timer 数，默认 128（8–1024）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_timer_storm_deadline/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4 TORTURE_TIMERS=512 /tmp/case
```
