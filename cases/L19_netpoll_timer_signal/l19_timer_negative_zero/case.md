---
id: l19_timer_negative_zero
dim: L19_netpoll_timer_signal
priority: P0
sources:
  - quic-go@v0.50.1: internal/utils/timer_test.go:100 已过期 deadline 连续 Reset 的读/未读状态机
  - grpc-go@v1.71.1: internal/idle/idle_test.go:133 空闲超时 timer 到期后回调置状态并通知
  - nats-server@v2.11.0: server/events_test.go:3173 连接计数归零时停止周期 timer，停止后不得再回调
tags: [L19, timer, zero-duration, negative-duration, reset, stop, panic, pointer, gc, UAF_SEED]
requires: []
runtime: "<1s"
---

# 场景

覆盖定时器时长边界：`NewTimer(0)`、`NewTimer(负值)`、`After(0)`、`AfterFunc(0)`
与 `AfterFunc(负值)` 必须立即就绪；已到期 timer 的 `Stop` 返回 false，零/负时长
`Reset` 返回 false 并重新立即排程；非正周期 `Ticker` 必须 panic。闭包捕获的
指针链在多次立即到期与显式 GC 之间保持正确。

## 期望行为

1. 零/负时长 Timer/After/AfterFunc 全部立即触发，无一挂起；
2. 已触发且读空的 timer `Stop` 返回 false；
3. 已到期/已停止 timer 的 `Reset(0)`/`Reset(负值)` 返回 false 并再次触发；
4. 活跃 1h timer 先 `Stop` 返回 true，再 `Reset(0)` 立即触发；
5. `NewTicker(0)` 与 `NewTicker(-1s)` 均 panic 且被 recover；
6. 退出码 0，末行 `ALL PASS l19_timer_negative_zero`。

## 失败判读

- 等待超时：零/负时长没有立即排程，timer 堆处理边界有误；
- `FAIL: Stop/Reset ... returned true`：timer 状态机返回值错误；
- Ticker 未 panic 或 panic 未被捕获：周期校验路径异常。

## 参数

- `UAF_SEED`：payload 基址种子；
- 无其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_timer_negative_zero/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
