---
id: l19_ticker_stop_reset
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - etcd@v3.5.0: server/lease/lessor_test.go:526 1s checkpoint ticker 周期回调，Stop 后停止
  - tailscale@v1.80.2: net/connstats/stats_test.go:50 Ticker 驱动周期 dump，Shutdown 时关闭 ticker
  - quic-go@v0.50.1: connection_test.go:1749 阻塞发送队列下反复重置 timer 以驱动事件循环
tags: [L19, ticker, stop, reset, concurrent-stop, pointer, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟 etcd lease checkpoint 与 tailscale connstats 的周期任务，并把 ticker 的 Stop/Reset 放大为状态机测试：

1. 首次 tick 前连续 Stop 两次，要求幂等、channel 不关闭；
2. 8 轮 `Reset(1ms) -> 收 tick -> Stop 两次 -> 排空`，每轮必须收到 tick；
3. 4 个 goroutine 各并发 Stop 两次，不得 panic 或死锁；
4. 并发 Stop 后 `Reset(1ms)` 必须重新开始投递；
5. ticker pending 时显式 `runtime.GC()`，随后仍能收到 tick。

每轮 GC 后校验持有对象上的三节点指针链。

# 期望行为

1. Stop 不关闭 `Ticker.C`，重复 Stop 无副作用；
2. 每次 Reset 后恰好能收到 tick，最终 ticks 计数为 10；
3. 并发 Stop 与 GC 后 ticker 仍可 Reset 并投递；
4. 最后一行输出 `ALL PASS l19_ticker_stop_reset`。

# 失败判读

- `ticker channel closed`：Stop 关闭了 channel 或投递路径异常；
- `ticker did not deliver a tick`：Reset 未重新武装 ticker；
- `ticks=... want=...`：多投递或漏投递；
- `payload corrupted`：ticker 持有对象在 GC 后损坏；
- 超时：Stop/Reset 状态机死锁。

# 参数

- `UAF_SEED`：payload base 种子（默认 1）；
- ticker 周期固定 1ms；所有等待使用 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_ticker_stop_reset/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
