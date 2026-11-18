---
id: l04_cond_sleep_wake
dim: L04_scheduler_stack
priority: P2
sources:
  - tailscale@v1.80.2: net/memnet/pipe_test.go:33 内存 pipe 用 sync.Cond + 计时器实现 Block/唤醒
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:141 首个回调阻塞时后续回调排队，关闭时不得越序
tags: [L04, sync.Cond, sleep-wake, signal, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_WAITERS 可放大）"
---

# 场景

模拟 tailscale memnet 管道与 gRPC callback serializer 的"阻塞—唤醒"队列：默认 24 个 waiter 在 `sync.Cond` 上以 `gen == 0` 为谓词睡眠，各自持有栈上 3 节点私有链。主协程在锁内确认全部 waiter 已进入 `Wait`（`waiting` 在 Wait 前于锁内递增），此时显式 GC；随后持锁把 `gen` 置 1 并逐个 `Signal`，每个 waiter 恰好被唤醒一次并复验私有链与共享链。

# 期望行为

1. 所有 waiter 都在固定自旋上限内入睡，`waiting == n`；
2. 每个 waiter 的唤醒状态恰好为 1，无丢唤醒、无重复；
3. 睡眠期间发生 GC 后，waiter 栈上的私有链仍为 `b, b+1, b+2`；
4. 主协程持有的共享链在全部唤醒并再次 GC 后仍完整；
5. 进程退出码为 0，最后一行输出 `ALL PASS l04_cond_sleep_wake`。

# 失败判读

- `only ... waiters slept`：waiter 未进入 Cond 队列，或锁协调有误；
- wait 返回后卡住：Signal 丢失（主协程未持锁或 waiter 尚未入睡）；
- `not woken`/`chain corrupted`：睡眠 goroutine 的栈/栈映射错误；
- 超时：Signal 数不足或 Cond 谓词条件错误。

# 参数

- `UAF_SEED`：链值种子；
- `TORTURE_ROUNDS`：轮数，默认 2（1–12）；
- `TORTURE_WAITERS`：每轮 waiter 数，默认 24（4–128）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_cond_sleep_wake/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4 TORTURE_WAITERS=64 /tmp/case
```
