---
id: l19_signal_multi_chan
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/signal_test.go:428 lame-duck 期间 TERM 不得提前终止进程，信号状态机必须可控
  - nats-server@v2.11.0: server/signal_test.go:470 lame-duck 收尾用 AfterFunc 看门狗 timer 兜底
  - etcd@v3.5.0: pkg/osutil/osutil_test.go:72 中断处理器注册到独立 signal 通道，自发信号验证计数
tags: [L19, signal, multi-channel, notify, stop, sigusr1, sigusr2, pointer, gc, linux-only]
requires: [signals, unix]
runtime: "<1s（仅 linux 执行真实信号路径）"
---

# 场景

模拟同一服务把信号接入多个通知通道的路径：SIGUSR1 同时注册到 chA 与 chB，
SIGUSR2 只注册到 chC。验证广播语义、单独 `signal.Stop(chA)` 后只有 chA 静默、
重新 `Notify` 后广播恢复，以及不同信号之间互不串扰。每次信号迁移前后显式 GC
并校验主 goroutine 栈上的指针链。

## 期望行为

1. 自发 SIGUSR1 后 chA、chB 各收到一次，chC 保持空；
2. `Stop(chA)` 返回后 chA 不再收到 SIGUSR1，chB 仍能收到；
3. 重新 `Notify(chA, SIGUSR1)` 后 chA 恢复接收；
4. SIGUSR2 只进入 chC，chA/chB 不得收到；
5. 最终计数为 chA=2、chB=3、chC=1，payload 链在 GC 前后不变；
6. 非 Linux 平台打印 SKIP 后仍输出 `ALL PASS l19_signal_multi_chan`；Linux 上
   最后一行输出该标记，失败退出码为 1。

## 失败判读

- `unexpected signal`：`Stop` 后注册未撤销，或不同信号未隔离；
- `signal ... not received`：广播路径丢投递；
- 进程被 SIGUSR1/SIGUSR2 默认动作终止：清理路径未保留信号处理；
- payload 损坏：信号交付期间的栈扫描/抢占破坏栈上指针。

## 参数

- `UAF_SEED`：随机路径种子，参与 payload 基值计算；
- 仅 Linux 执行真实 `syscall.Kill` 路径。

## 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_signal_multi_chan/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
