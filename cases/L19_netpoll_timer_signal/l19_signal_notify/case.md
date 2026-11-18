---
id: l19_signal_notify
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/signal_test.go:428 信号/优雅关闭路径（TERM 不得提前终止进程）
tags: [L19, signal, sigusr1, syscall-kill, pointer, gc, linux-only]
requires: [signals, unix]
runtime: "<1s（仅 linux 执行真实信号路径；其他平台打印 SKIP 后仍输出 ALL PASS）"
---

# 场景

模拟 NATS 服务器的信号处理路径：`signal.Notify` 注册 `SIGUSR1`，主协程用 `syscall.Kill(os.Getpid(), syscall.SIGUSR1)` 自发自收。为保证标准信号不被合并，采用"发一个、确认收到、再发下一个"的顺序，共 16 个。每个信号前后都分配垃圾并显式 `runtime.GC()`，校验闭包/栈上的三节点指针链；`signal.Stop` 后确认没有多余信号、计数不变。

本用例使用真实 Unix 信号，仅在 Linux 上执行；其他平台（`runtime.GOOS != "linux"`）打印 `SKIP:` 行后仍按本仓约定输出 `ALL PASS`，不判失败。

# 期望行为

1. 16 次自发自收全部成功，`received == 16`；
2. 每次收到的信号值均为 `syscall.SIGUSR1`；
3. 信号前后 GC 后 payload 指针链内容完整；
4. `signal.Stop` 后 channel 中无残留信号，计数不变；
5. 最后一行输出 `ALL PASS l19_signal_notify`。

# 失败判读

- `signal ... not received`：信号注册/投递路径异常或进程无法对自身发信号；
- `unexpected signal or corrupted payload`：信号值错误或 GC 后指针损坏；
- `extra signal after Stop`：Stop 语义异常；
- 非 Linux 平台的 `SKIP:` 行属于预期路径，runner 仍会看到 ALL PASS。

# 参数

- `UAF_SEED`：payload base 种子（默认 1）；
- 固定 16 个信号；每个信号等待 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_signal_notify/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
