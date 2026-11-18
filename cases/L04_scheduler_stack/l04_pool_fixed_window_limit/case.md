---
id: l04_pool_fixed_window_limit
dim: L04_scheduler_stack
priority: P2
sources:
  - tailscale@v1.80.2: tstime/rate/rate_test.go:77 TestLimiterBurst1 以 burst 容量限制同一时刻可消费的令牌数
  - echo@v4.13.4: middleware/rate_limiter_test.go:310 TestRateLimiterMemoryStore_Allow 按 burst 放行并拒绝超限请求
tags: [L04, worker-pool, rate-limit, fixed-window, concurrency-cap, channel, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认 6 窗口，TORTURE_ROUNDS 可放大）"
---

# 场景

模拟 worker 池上的固定窗口限流：8 个 worker 消费 36 个任务，但并发执行受容量 4 的 sem 闸门限制；协调者按窗口发放 token，每窗口恰好 6 个，收齐 6 个“已拿到 token 并开跑”的确认后才发放下一窗口，全程无 wall-clock 计时，窗口推进完全由确认计数驱动。前 4 个任务在 runGate 上阻塞时主协程显式 GC；协调者在每个奇数窗口后也显式 GC。窗口数由 `TORTURE_ROUNDS` 放大。

# 期望行为

1. 总任务 36（或 6×窗口数）全部完成且只完成一次；
2. 运行中 active 峰值 ≤ 4（sem 容量），且至少为 1；
3. 每个窗口 token 数恰为 6，全部窗口计数正确；
4. 窗口 id 在开跑确认序列中单调且与协调者当前窗口一致；
5. 每个任务校验和等于独立公式计算结果；
6. 运行中 GC 与最终 GC 后，所有任务保留的 3 节点链完整；
7. 最后一行输出 `ALL PASS l04_pool_fixed_window_limit`。

# 失败判读

- `peak concurrency > limit`：sem 获取/释放顺序错误，池限流失效；
- 窗口计数不符：token 多发/少发或任务重复消费；
- `start ack window` 不匹配：窗口令牌乱序/提前发放；
- 任务和错误：GC 后任务指针或校验字段被破坏；
- `active != 0`：完成计数与 sem 释放不平衡；
- 超时：runGate 未放行、coordinator 等待不到开跑确认。

# 参数

- `UAF_SEED`：任务链基值与步长（默认 1）；
- `TORTURE_ROUNDS`：窗口数倍率，窗口数 = 6 × 该值（默认 1，即 6 窗口 / 36 任务）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_pool_fixed_window_limit/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=4 TORTURE_ROUNDS=10 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
