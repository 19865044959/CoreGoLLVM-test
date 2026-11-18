---
id: l19_multi_timer_cancel
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 100 个并发等待者中随机取消 10 个，其余按限流放行
  - grpc-go@v1.71.1: internal/cache/timeoutCache_test.go:51 TTL cache 删除 entry 时 Stop 对应 timer
  - nats-server@v2.11.0: server/events_test.go:3173 连接计数归零时停止周期 timer，停止后不得再回调
tags: [L19, timer-cancel, stop, concurrent, afterfunc, pointer, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟 quic-go 并发等待者取消与 gRPC TTL cache 删除时 Stop timer 的路径：

1. 128 个 1ms `AfterFunc` 回调启动后阻塞在 gate 上，主协程确认全部进入阻塞态后显式 GC（payload 在回调栈上）；
2. 另外 512 个 1 小时 `AfterFunc` timer 由 4 个 goroutine 并发 `Stop`，全部必须返回 true；
3. 对已停止 timer 重复 Stop 必须返回 false；
4. 回调仍阻塞时再次 GC，随后放行 gate，128 个回调恰好完成；
5. 哨兵 timer 触发并再次 GC 后，被取消的 timer 回调次数必须为 0。

# 期望行为

1. 512 个并发取消全部成功，重复 Stop 无 true；
2. 放行后 128 个回调恰好完成，被取消的 512 个回调为 0；
3. 所有 GC 检查点 payload 指针链完整；
4. 最后一行输出 `ALL PASS l19_multi_timer_cancel`。

# 失败判读

- `stopped=... want=...`：并发 Stop 丢失取消；
- `fired=...` / `canceled timers fired=...`：timer 触发次数异常，timer heap 摘除/调度错误；
- `payload corrupted`：回调栈或闭包捕获的指针在 GC 后失效；
- 超时：回调未启动或 gate 协调死锁。

# 参数

- `UAF_SEED`：payload base 种子（默认 1）；
- 固定 128 个触发 timer、512 个取消 timer、4 个取消 goroutine；所有等待使用 10s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_multi_timer_cancel/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
