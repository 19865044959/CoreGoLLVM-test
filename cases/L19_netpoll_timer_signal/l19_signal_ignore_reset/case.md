---
id: l19_signal_ignore_reset
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/signal_test.go:428 lame-duck 期间 TERM 不得提前终止进程，信号状态机必须可控
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 同类阻塞唤醒路径中信号/关闭状态切换
tags: [L19, signal, signal-ignore, signal-reset, sigusr1, sigusr2, pointer, gc, linux-only]
requires: [signals, unix]
runtime: "<1s（仅 linux 执行真实信号路径）"
---

# 场景

模拟 NATS 信号状态机中“忽略信号/恢复默认动作”的切换：

1. `signal.Notify` 注册 SIGUSR1/SIGUSR2，自发 SIGUSR1 验证基线投递；
2. `signal.Ignore(SIGUSR1)`（调用两次验证幂等）后连发 4 个 SIGUSR1，进程不得退出、channel 不得收到；
3. 以两次 SIGUSR2 自发自收作为顺序屏障，确认被忽略的 SIGUSR1 没有混入 channel；
4. `signal.Reset(SIGUSR1)` 后立即重新 `Notify`，再发 2 个 SIGUSR1 必须正常收到。

每个阶段之间显式 `runtime.GC()` 并校验闭包/栈上的三节点指针链。

# 期望行为

1. 基线 SIGUSR1 恰好收到 1 次；
2. Ignore 期间 4 个 SIGUSR1 不投递、进程存活，`ignoredStray == 0`；
3. Reset+Notify 后 2 个 SIGUSR1 全部收到，无多余信号；
4. 所有 GC 检查点 payload 指针链完整；
5. 最后一行输出 `ALL PASS l19_signal_ignore_reset`（非 linux 打印 `SKIP:` 后同样输出）。

# 失败判读

- `ignored SIGUSR1 was delivered`：signal.Ignore 未撤销 Notify 或仍投递；
- `SIGUSR2 barrier not received`：信号通道/屏障路径异常；
- `signal lost after Reset+Notify`：signal.Reset 未恢复可通知状态；
- `payload corrupted`：信号处理路径捕获的指针在 GC 后失效；
- 进程被 SIGUSR1 终止：Reset 后默认动作未按预期恢复（runner 以退出码捕获）。

# 参数

- `UAF_SEED`：payload base 种子（默认 1）；
- 固定 4 次忽略发送、2 个屏障信号、2 次恢复发送；所有等待使用 5s 上限，仅防死锁，不做耗时断言。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_signal_ignore_reset/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GODEBUG=clobberfree=1 /tmp/case
```
