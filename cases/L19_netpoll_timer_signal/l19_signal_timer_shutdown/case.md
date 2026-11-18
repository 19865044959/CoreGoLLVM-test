---
id: l19_signal_timer_shutdown
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/signal_test.go:428 lame-duck 期间 TERM 不得提前终止进程，信号状态机必须可控
  - nats-server@v2.11.0: server/signal_test.go:470 信号收尾路径用 3s AfterFunc 看门狗兜底
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 timer 到期/Reset 与事件循环交替
tags: [L19, signal, timer, afterfunc-watchdog, select, graceful-shutdown, sigusr1, pointer, gc, linux-only]
requires: [signals, unix]
runtime: "<2s（仅 linux 执行真实信号路径）"
---

# 场景

模拟优雅关闭时信号、看门狗 timer 与工作队列合流到同一 `select` 的状态机：

1. 偶数轮：先自发 SIGUSR1，事件循环在消费信号的同时必须处理完固定 8 个
   带指针链的任务；之后 1h 看门狗 `Stop` 返回 true 且回调 0 次；
2. 奇数轮：不发信号，零延时看门狗 AfterFunc 必须触发并关闭信号通道外的
   watchCh，事后 `Stop` 返回 false，信号通道保持空闲；
3. 每轮在任务处理中点与收尾显式 GC，校验任务/看门狗闭包捕获的指针链。

# 期望行为

1. 4 个信号轮全部收到 SIGUSR1，消费信号数恰为 4；
2. 每轮 8 个任务按 id 顺序处理，payload 链在 GC 后完整；
3. 信号轮的 1h 看门狗 `Stop` 全部返回 true，回调 0 次；
4. timer 轮的看门狗恰好回调 1 次，`Stop` 返回 false，无游离信号；
5. 全程无 select 饥饿/死锁；末行 `ALL PASS l19_signal_timer_shutdown`；
6. 非 Linux 平台打印 `SKIP:` 后仍输出 `ALL PASS`。

# 失败判读

- `shutdown select stuck`：信号/任务/看门狗合流丢唤醒或 select 饥饿；
- `watchdog fired while signal path was taken`：Stop 语义错误；
- `watchdog callbacks=... want=...`：定时器丢失或重复触发；
- `job payload corrupted`：任务链或看门狗闭包指针在 GC 后失效；
- `stray signal`：跨轮信号残留，发送/消费未串行化。

# 参数

- `UAF_SEED`：每轮 payload 基址种子；
- 固定 8 轮（4 信号轮 + 4 timer 轮）、每轮 8 个任务；5s 上限仅防死锁；
- 仅 Linux 执行真实 `syscall.Kill` 路径。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_signal_timer_shutdown/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
