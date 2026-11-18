---
id: l19_ticker_worker
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - etcd@v3.5.0: server/lease/lessor_test.go:526 1s checkpoint ticker 周期回调，Stop 后停止
  - tailscale@v1.80.2: net/connstats/stats_test.go:50 Ticker 驱动周期 dump，Shutdown 时关闭 ticker
tags: [L19, ticker, worker-loop, pointer, gc, stop, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟 etcd lease checkpoint 与 tailscale connstats 的周期任务：一个 worker goroutine 独占 `time.Ticker` 与状态对象，状态包含计数、累计和、历史切片以及一条指针链。每 16 个 tick 在 worker 循环内分配垃圾并显式 GC；达到 128 个 tick 后 worker 自己调用 `Ticker.Stop()`、把最终状态交给主协程并退出。主协程在 Stop 后再次 GC，确认计数不再变化。

# 期望行为

1. worker 恰好处理 128 个 tick（`n == 128`），历史长度一致；
2. 每 tick 的值均为 `n*37 + seed*13`，累计和等于独立计算值；
3. 循环内 GC 后指针链内容完好，无损坏标记；
4. Stop 后计数/累计和/最后一项在再次 GC 前后保持不变；
5. 最后一行输出 `ALL PASS l19_ticker_worker`。

# 失败判读

- `ticks=... want=...`：ticker 停止语义错误或 worker 多处理/漏处理 tick；
- `history/count changed after Stop`：Stop 后仍有 goroutine 修改状态；
- `anchor chain corrupted`：worker 栈/堆指针在 GC 后损坏；
- 超时：ticker 未送达或 worker 死锁。

# 参数

- `UAF_SEED`：tick 值种子（默认 1）；
- 其余固定：ticker 1ms、目标 128 tick、每 16 tick 一次显式 GC。等待完成使用 10s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_ticker_worker/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 UAF_SEED=2 /tmp/case
```
