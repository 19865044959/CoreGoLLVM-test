---
id: l19_signal_timer_select
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/signal_test.go:428 lame-duck 期间的信号状态机与定时收尾
  - nats-server@v2.11.0: server/signal_test.go:470 信号收尾路径用 3s AfterFunc 看门狗兜底
  - quic-go@v0.50.1: internal/utils/timer_test.go:40 timer Reset/到期与事件循环的交替
tags: [L19, signal, timer, select, sigusr1, stop, pointer, gc, linux-only]
requires: [signals, unix]
runtime: "<2s（仅 linux 执行真实信号路径）"
---

# 场景

模拟事件循环把信号通道与长周期定时器合流到同一个 `select`：每轮先排程 1 小时
timer，再自发 SIGUSR1，`select` 必须走信号分支，timer 不得命中且 `Stop` 返回 true。
全部轮次后验证反向路径——无信号时 1ms timer 分支先就绪，信号通道无残留。

## 期望行为

1. 8 轮中每轮信号分支命中，收到 `SIGUSR1`，1h timer 的 `Stop` 均返回 true；
2. 信号通道只收到 8 个信号，无合并遗漏；
3. 空闲信号通道下 1ms timer 分支先就绪，`Stop` 返回 false；
4. 事件循环外非阻塞读取信号通道必须为空；
5. 每轮 GC 后 payload 指针链正确；退出码 0，末行 `ALL PASS l19_signal_timer_select`；
6. 非 Linux 平台打印 `SKIP:` 后仍输出 `ALL PASS`。

## 失败判读

- timer 分支抢先：信号未及时投递或 select 就绪判断异常（超时值仅作死锁保护）；
- `signals=N want=8`：标准信号被合并，说明发送/消费未按轮次串行；
- 残留信号：上一轮信号未被消费，计数协调失效。

## 参数

- `UAF_SEED`：payload 基址种子；
- 无其他 TORTURE 参数。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_signal_timer_select/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
