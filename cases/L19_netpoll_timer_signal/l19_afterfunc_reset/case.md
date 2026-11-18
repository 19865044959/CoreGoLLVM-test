---
id: l19_afterfunc_reset
dim: L19_netpoll_timer_signal
priority: P0
sources:
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 10 次 Reset 交替读 channel 与过窗，最后一次仍须触发
  - quic-go@v0.50.1: internal/utils/timer_test.go:100 同一 deadline 下 Stop/Reset 与读/未读状态机
  - grpc-go@v1.71.1: internal/transport/keepalive_test.go:52 空闲 timer 到期望值后回调并重置状态
tags: [L19, afterfunc, timer-reset, callback-state, pointer, gc, UAF_SEED]
requires: []
runtime: "<1s"
---

# 场景

模拟 quic-go 连接 timer 包装器与 gRPC keepalive 的 `AfterFunc` 使用方式，逐项验证 `Reset` 语义：

1. 活跃 timer `Reset(1ms)` 返回 true，只触发一次；
2. 已触发 timer `Reset(1ms)` 返回 false，重新武装后再触发一次；
3. `Stop` 返回 true 后 `Reset` 返回 false 并重新武装；
4. 回调内部对自身 `Reset(1ms)`，连续 8 次触发；
5. 回调阻塞在 gate 上时从外部 `Reset(1ms)` 返回 false，放行后恰好再触发一次。

每次状态迁移与 GC 后校验闭包捕获的三节点指针链。

# 期望行为

1. 五组 Reset 返回值与触发次数全部符合上述语义；
2. 每种状态下 payload 指针链在 `runtime.GC()` 后内容完整；
3. 回调内自 Reset 恰好 8 次，阻塞回调场景恰好 2 次，无重复触发；
4. 最后一行输出 `ALL PASS l19_afterfunc_reset`。

# 失败判读

- `Reset on active/fired/stopped ... returned ...`：Reset 返回值语义与 Go 运行时契约不符；
- `... fires=... want=...`：timer 丢失或重复触发，timer heap 状态机异常；
- `payload corrupted`：闭包捕获的指针在 GC 后失效；
- 超时：回调未调度或 gate 协调死锁。

# 参数

- `UAF_SEED`：payload base 种子（默认 1）；
- 固定各阶段 1ms 武装；所有等待使用 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_afterfunc_reset/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
