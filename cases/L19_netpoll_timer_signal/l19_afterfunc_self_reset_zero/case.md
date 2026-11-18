---
id: l19_afterfunc_self_reset_zero
dim: L19_netpoll_timer_signal
priority: P0
sources:
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 TestTimerMultipleResets 10 次 Reset 交替读取 channel 与过窗
  - quic-go@v0.50.1: internal/utils/timer_test.go:77 TestTimerPastDeadline 已过期 deadline 必须立即触发
  - quic-go@v0.50.1: internal/utils/timer_test.go:88 TestTimerZeroDeadline 零 deadline 的 timer 不排程语义
tags: [L19, afterfunc, zero-duration, negative-duration, self-reset, timer-heap, pointer, gc, UAF_SEED]
requires: []
runtime: "<1s"
---

# 场景

覆盖零/负时长 `AfterFunc` 与回调内自重置的组合：零延时回调每触发一次就用
`Reset(0)` 重新武装自身，固定 32 次；负延时回调以 `Reset(-1s)` 自重置固定
8 次。对已到期（正在回调）的 timer，`Reset` 必须返回 false 且重新立即排程；
循环结束时的 `Stop` 必须返回 false。回调内每 4/8 次显式 GC，主协程在哨兵
零 timer 触发后再次 GC，校验闭包捕获的三节点指针链并确认无多余回调。

# 期望行为

1. 零延时自重置回调恰好触发 32 次，前 31 次 `Reset(0)` 均返回 false；
2. 负延时自重置回调恰好触发 8 次，前 7 次 `Reset(-1s)` 均返回 false；
3. 两条链最后一次回调内 `Stop` 返回 false，之后计数不再增加；
4. 链结束后主协程 `Stop` 返回 false，无重复/迟到回调；
5. 所有 GC 检查点 payload 指针链完整（`base/base+1/base+2`）；
6. 最后一行输出 `ALL PASS l19_afterfunc_self_reset_zero`，失败退出码为 1。

# 失败判读

- `zero/negative fired=...`：自重置链中断或重复触发，timer 状态机异常；
- `resetFalse=...`：已到期 timer 的 Reset 返回值语义不符；
- `Stop on fired ... returned true`：到期状态判断错误；
- `payload corrupted`：timer 闭包捕获的指针在 GC 后失效；
- 超时：零/负时长未立即排程或回调调度丢失。

# 参数

- `UAF_SEED`：两条 payload 链基址种子；
- 固定 32/8 次自重置；5s 上限仅防死锁，不做耗时阈值断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_afterfunc_self_reset_zero/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1 /tmp/case
```
