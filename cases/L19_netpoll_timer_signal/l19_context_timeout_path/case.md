---
id: l19_context_timeout_path
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - grpc-go@v1.71.1: test/context_canceled_test.go:37 客户端 deadline 与状态写竞争，最终状态与附属数据必须一致
  - grpc-go@v1.71.1: internal/idle/idle_test.go:133 空闲超时 timer 到期后回调置状态并通知
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 timer 到期与 Reset 交替时的生命周期
tags: [L19, context, with-timeout, cancel, afterfunc, timer, pointer, gc, UAF_SEED]
requires: []
runtime: "<2s"
---

# 场景

模拟 gRPC 客户端 deadline 与 idle timer 路径：

1. 32 轮 `context.WithTimeout(1ms)`，worker 阻塞在 `ctx.Done()` 上，主协程在等待期间显式 GC 并校验 `context.WithValue` 携带的三节点指针链；到期后 `Err()` 必须为 `DeadlineExceeded`；
2. 父 ctx 显式取消时，1 小时超时的子 ctx 必须以 `context.Canceled` 结束而不是等待到期；
3. 负超时的 `WithTimeout` 立即到期，`Done` 已关闭且 `Err()` 为 `DeadlineExceeded`；
4. `context.AfterFunc` 随超时执行一次，执行后 `stop()` 返回 false；
5. 取消前 `stop()` 返回 true，之后 cancel 不得再启动回调。

全程穿插显式 GC 与垃圾分配，失败信息带 `UAF_SEED`。

# 期望行为

1. 32 轮超时路径全部返回 `DeadlineExceeded`，payload 指针链完整且 `CancelFunc` 幂等；
2. 父取消路径返回 `context.Canceled`，payload 完整；
3. 已过期 ctx 的 `Done` 已关闭；
4. AfterFunc 超时回调恰好执行一次；提前 stop 的回调执行 0 次；
5. 最后一行输出 `ALL PASS l19_context_timeout_path`。

# 失败判读

- `timeout err=... want=DeadlineExceeded`：context timer 路径未按时到期或错误值错误；
- `parent cancel err=...`：父取消未传播到子 ctx；
- `payload corrupted`：context value 中的指针在 GC 后失效；
- `stopped AfterFunc ran ...`：stop 语义异常；
- 超时：`ctx.Done` 未关闭或 worker 协调死锁。

# 参数

- `UAF_SEED`：payload base 种子（默认 1）；
- 固定 32 轮 1ms 超时；所有等待使用 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_context_timeout_path/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
