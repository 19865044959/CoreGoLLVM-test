---
id: l03_ticker_tight_loop
dim: L03_async_preempt
priority: P1
sources:
  - fiber@v2.52.6: utils/time_test.go:17 后台 goroutine 每秒刷新时间戳，读取端无锁
  - tailscale@v1.80.2: net/connstats/stats_test.go:50 周期 ticker 回调 dump 与 Shutdown
tags: [L03, async-preempt, ticker, tight-loop, gc, timer]
requires: []
runtime: "<10s"
---

# 场景

ticker goroutine 每个滴答分配垃圾并执行 `runtime.GC()`，固定完成 4 个滴答后关闭 done；主 goroutine 同时跑持 `*[64]int64` 的无调用紧循环（64 块 × 65536 次）。主循环结束或 ticker 完成后双方汇合，只对计数与校验和断言，不对实际耗时做断言。

# 期望行为

1. ticker goroutine 恰好完成 4 次 GC；
2. 主循环累加和等于 `total*64`（每 64 次迭代闭式值 4096）；
3. GC 后底层数组的 64 个元素保持不变；
4. 进程退出码为 0，最后一行 `ALL PASS l03_ticker_tight_loop`；
5. 各 profile 下结果一致。

# 失败判读

- `FAIL: ticker-driven GC count`：ticker 循环未按预期执行（定时器/调度异常）；
- `FAIL: tight-loop checksum while ticker GCs`：紧循环累加被破坏；
- `FAIL: pointer target intact after ticker GC`：持指针未保活；
- 超时：ticker 未触发或与主循环互等。

# 参数

无随机路径。ticker 间隔 1ms 仅决定 GC 节奏，断言不依赖实际耗时；GC 滴答数与循环规模固定。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_ticker_tight_loop/*.go
GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1,asyncpreemptoff=0 /tmp/case
```
