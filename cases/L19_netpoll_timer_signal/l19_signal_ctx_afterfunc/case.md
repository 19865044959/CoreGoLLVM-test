---
id: l19_signal_ctx_afterfunc
dim: L19_netpoll_timer_signal
priority: P1
sources:
  - nats-server@v2.11.0: server/signal_test.go:470 lame-duck 收尾路径用 AfterFunc 看门狗兜底
  - etcd@v3.5.0: pkg/osutil/osutil_test.go:72 中断处理器注册到独立 signal 通道，自发信号验证计数
  - grpc-go@v1.71.1: test/context_canceled_test.go:37 取消与状态写竞争，最终状态与附属数据必须一致
tags: [L19, signal, notify-context, context-afterfunc, cancel, worker-drain, sigusr1, sigusr2, pointer, gc, linux-only]
requires: [signals, unix]
runtime: "<1s（仅 linux 执行真实信号路径）"
---

# 场景

模拟服务把信号接入 context 生命周期并挂接清理回调：`signal.NotifyContext`
监听 SIGUSR1，`context.AfterFunc` 注册清理回调，worker 阻塞在 `ctx.Done()` 上
并从 `context.WithValue` 读取三节点指针链。自发 SIGUSR1 后 ctx 必须以
`context.Canceled` 结束，清理回调与 worker 各执行一次，回调后 stop 返回
false。另建只听 SIGUSR2 的 NotifyContext 验证互不误取消；父 ctx 取消必须
传播；stop 可重复调用；取消前 stop 的 AfterFunc 不得执行。

# 期望行为

1. 信号前 ctx 未取消；SIGUSR1 后 `ctx.Err()` 为 `context.Canceled`；
2. AfterFunc 清理回调恰好执行 1 次，执行后 `stop` 返回 false；
3. worker 在 GC 后读到完整 payload 链并返回 `context.Canceled`；
4. SIGUSR1 不取消 SIGUSR2 的 NotifyContext；父取消正确传播到子 ctx；
5. 重复/并发 stop 不 panic；取消前被 stop 的 AfterFunc 执行 0 次；
6. 每步 GC 后 payload 链正确；Linux 末行 `ALL PASS l19_signal_ctx_afterfunc`，
   非 Linux 打印 `SKIP:` 后同样输出该标记。

# 失败判读

- `context canceled before signal`：信号未到 ctx 已被误取消；
- `ctx.Err=... want context.Canceled`：信号驱动 cancel 路径异常；
- `AfterFunc ran ... times`：清理回调丢失或重复执行；
- `worker payload corrupted`：context value / done 路径上的指针被误回收；
- 错取消另一 ctx 或父取消未传播：NotifyContext 路由/继承缺陷；
- 进程被默认信号动作终止：signal 注册/清理路径异常。

# 参数

- `UAF_SEED`：payload 链基址种子；
- 固定 1 个 SIGUSR1；5s 上限仅防死锁；仅 Linux 执行真实信号路径。

# 运行

```bash
go build -o /tmp/case ./cases/L19_netpoll_timer_signal/l19_signal_ctx_afterfunc/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1 /tmp/case
```
