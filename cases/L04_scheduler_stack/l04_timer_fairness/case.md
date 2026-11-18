---
id: l04_timer_fairness
dim: L04_scheduler_stack
priority: P2
sources:
  - grpc-go@v1.71.1: internal/idle/idle_test.go:133 空闲超时 timer 触发后 manager 调用 enforcer 进入 idle
  - tailscale@v1.80.2: wgengine/watchdog_test.go:15 连续请求后 Close，watchdog timer 不得误报超时
  - tailscale@v1.80.2: ipn/ipnlocal/bus_test.go:123 限速 bus 的 timer flush 与窗口内合并
tags: [L04, timer, fairness, round-robin, sync.Cond, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_WORKERS 可放大）"
---

# 场景

模拟 gRPC idle timer 与 tailscale watchdog/限速 bus 的定时唤醒路径，并叠加轮转公平性：默认 8 个 worker 通过 `sync.Cond` 按 `turn%workers` 严格轮转，每个 worker 每轮拿到资格后在 `time.NewTimer(1–3ms)` 上阻塞，醒来后校验自己 3 节点指针链并累加。每个 worker 分两阶段各执行 `6×TORTURE_ROUNDS` 轮；第一阶段结束时全部停在 barrier，主协程显式 GC 两次后放行，第二阶段结束后再 GC 一次。轮转由共享计数强制，timer 唤醒顺序不影响公平性判定。

# 期望行为

1. 共享 turn 计数最终等于 `workers × 2 × perPhase`，每个 worker 获得完全相同的轮数；
2. 每次 timer 唤醒后，worker 的 3 节点链校验和等于 `3*base+21`；
3. 每个 worker 的累加器等于 `2*perPhase*want + perPhase*(2*perPhase-1)`；
4. 两处 GC（阶段间与最终）后指针链仍完整；
5. 所有 worker 各返回一次结果，无饥饿导致的缺失；
6. 最后一行输出 `ALL PASS l04_timer_fairness`。

# 失败判读

- `turn count=...`：轮转协议被破坏，可能有 worker 多吃/少吃轮次；
- `acc=... want=...`：timer 唤醒后本地状态被覆盖或 worker 未按编号取得轮次；
- `pointer chain corrupted`：GC 打断 timer 回调栈时指针槽扫描错误；
- 超时：`sync.Cond` 的 Broadcast 丢失或 timer 未触发导致轮转停摆。

# 参数

- `UAF_SEED`：timer 时长偏移与链基值种子（默认 1）；
- `TORTURE_ROUNDS`：每阶段每 worker 轮数的倍数，默认 2，范围 1–20；
- `TORTURE_WORKERS`：worker 数，默认 8，范围 2–32。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_timer_fairness/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_ROUNDS=5 TORTURE_WORKERS=16 GOGC=1 GOMAXPROCS=2 /tmp/case
```
