---
id: l04_timer_coalesce_bus
dim: L04_scheduler_stack
priority: P2
sources:
  - tailscale@v1.80.2: ipn/ipnlocal/bus_test.go:123 限速 bus：窗口内后续 Notify 合并为最新值，flush timer 推进后统一投递
  - nats-server@v2.11.0: server/events_test.go:3173 周期 timer 上报在计数归零时停止，验证 timer 生命周期
tags: [L04, timer, coalesce, merge, timer-heap, goroutine, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_WORKERS/TORTURE_ITEMS 可放大）"
---

# 场景

模拟限速事件总线：多个 notifier 各自反复更新共享 pending 指针链，flusher goroutine 用真实 `time.Timer` 合并窗口，只投递窗口内最后一次更新。notifier 在中点屏障阻塞（pending 链由 bus 持有）时显式 GC；关闭时 `stop` 分支强制 flush 尾巴并停表。

# 期望行为

1. 投递的代号严格递增且不重复，最终投递代号等于更新总数（尾巴不丢）；
2. 每次投递的链（`next.v == v+1`、同代号）完整，GC 后仍可读；
3. 投递次数 ≥1 且不超过更新总数（合并只允许减少投递，不允许凭空产生）；
4. notifier 的所有更新对象在 GC 后仍完整（未投递的只允许被合并覆盖，不允许悬空）；
5. 关闭后 flusher goroutine 与 timer 正常退出，无挂起；
6. 最后一行输出 `ALL PASS l04_timer_coalesce_bus`。

# 失败判读

- “delivered gen ... after ...”：timer 回调投递了过期 pending 或重复投递；
- “last delivered gen ...”：stop/force flush 路径漏掉最后一次更新；
- chain corrupted：timer 堆或关闭路径上的闭包/指针在 GC 后失效；
- 超时：stop 后 flusher 未退出或 timer 未被 Stop。

# 参数

- `UAF_SEED`：更新链基值与轮次扰动（默认 1）；
- `TORTURE_ROUNDS`：轮数（默认 2，范围 1..10）；
- `TORTURE_WORKERS`：notifier 数（默认 6，范围 2..16）；
- `TORTURE_ITEMS`：每个 notifier 的更新次数（默认 24，范围 4..128）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_timer_coalesce_bus/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 TORTURE_ROUNDS=6 TORTURE_WORKERS=12 TORTURE_ITEMS=96 GOGC=1 GOMAXPROCS=2 /tmp/case
```
