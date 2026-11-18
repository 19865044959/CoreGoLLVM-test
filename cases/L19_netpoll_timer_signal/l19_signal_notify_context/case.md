---
id: l19_signal_notify_context
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/signal_test.go:428 lame-duck 模式下的受控信号处理，信号不得提前终止进程
  - nats-server@v2.11.0: server/signal_test.go:470 信号收尾路径用 time.AfterFunc 看门狗兜底
  - etcd@v3.5.0: pkg/osutil/osutil_test.go:72 中断处理器注册到独立 signal 通道并自发信号验证计数
tags: [L19, signal, notify-context, context-cancel, sigusr1, sigusr2, pointer, gc, linux-only]
requires: [signals, unix]
runtime: "<1s（仅 linux 执行真实信号路径）"
---

# 场景

模拟服务把信号接入 `context` 生命周期的路径：SIGUSR1/SIGUSR2 分别注册到两个
独立的 `signal.NotifyContext`，只取消对应 ctx，另一 ctx 不得被误取消；父 context
取消时子 NotifyContext 也必须以 `context.Canceled` 结束。`stop` 可重复/并发调用，
取消原因保持不变。每次信号迁移前后显式 GC 校验指针链。

## 期望行为

1. SIGUSR1 只关闭 ctxC 的 `Done`，`Err()` 为 `context.Canceled`；ctxD 保持活跃；
2. SIGUSR2 只关闭 ctxD 的 `Done`，原因同为 `context.Canceled`；
3. 父 ctx 取消时 ctxP 立即结束，无需信号；
4. stop 重复与并发调用不 panic，三个 ctx 的 Done 状态与取消原因不变；
5. 每步 GC 后 payload 指针链正确；退出码 0，末行 `ALL PASS l19_signal_notify_context`；
6. 非 Linux 平台打印 `SKIP:` 后仍输出 `ALL PASS`。

## 失败判读

- 错把另一 ctx 取消：信号到 NotifyContext 的路由/广播处理错误；
- ctx 未结束或 `Err()` 非 Canceled：信号驱动的 cancel 路径未执行；
- 并发 stop panic：stop 幂等性缺陷；
- 其他平台真实信号不可用属预期，按 SKIP 处理。

## 参数

- `UAF_SEED`：payload 基址种子；
- 无其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_signal_notify_context/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
