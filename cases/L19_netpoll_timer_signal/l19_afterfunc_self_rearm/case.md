---
id: l19_afterfunc_self_rearm
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 10 次 Reset 交替读 channel 与过窗，最后一次仍须触发
  - quic-go@v0.50.1: internal/utils/timer_test.go:100 同一 deadline 下 Stop/Reset 与读/未读状态机
  - grpc-go@v1.71.1: internal/cache/timeoutCache_test.go:51 TTL cache 到期回调与 Stop 对应 timer
tags: [L19, afterfunc, self-reset, callback, stop, timer-heap, pointer, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟连接级定时器在回调内部自我重新武装的路径：`time.AfterFunc` 回调每触发一次
就对自身 `Reset`，直到固定 64 次；已到期（正在回调）的 timer 上 `Reset`/`Stop`
必须返回 false，`Reset` 重新排程、`Stop` 不改变后续行为。另一组验证回调内
`Stop` 自身后再由外部 `Reset` 能再触发一次。闭包捕获的指针链在 timer 堆与
回调 goroutine 栈之间必须存活。

## 期望行为

1. 自我 Reset 循环恰好触发 64 次，前 63 次 `Reset` 均返回 false；
2. 第 64 次回调内 `Stop` 返回 false，之后计数不再变化；
3. 回调内 `Stop` 后再外部 `Reset` 恰好再触发一次，总触发数为 2；
4. 两组闭包捕获的指针链在显式 GC 后内容正确；
5. 退出码 0，末行 `ALL PASS l19_afterfunc_self_rearm`。

## 失败判读

- 触发次数多/少：`Reset` 返回值语义或 timer 到期状态判断错误；
- `FAIL: ... payload corrupted`：回调闭包捕获对象被 GC 错误回收；
- 超时：self-rearm 链中断，timer 不再被排程。

## 参数

- `UAF_SEED`：两组指针链基址种子；
- 无其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_afterfunc_self_rearm/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
