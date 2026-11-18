---
id: l02_chan_semaphore_tokens
dim: L02_concurrency
priority: P1
sources:
  - tailscale@v1.80.2: syncs/syncs_test.go:145 容量 2 的 Semaphore：Acquire/TryAcquire/Release，已取消 ctx 的 Acquire 立即失败
  - kitex@v0.13.0: pkg/remote/trans/nphttp2/grpc/flowcontrol_test.go:27 writeQuota.get 配额不足时阻塞，replenish 后返回
  - quic-go@v0.50.1: streams_map_outgoing_test.go:405 100 个并发 OpenStreamSync，按限流逐步放行
tags: [L02, channel-semaphore, token, TryAcquire, context-cancel, no-token-leak, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 tailscale Semaphore 与 kitex writeQuota 的 token 语义，但用 buffered channel 直接承载令牌：Acquire 从 channel 接收令牌，Release 归还，TryAcquire 走 `select/default`。验证四个边界：

1. TryAcquire 在容量内成功、耗尽后失败，归还后恢复；
2. 3 个 holder 同时持令牌，in-flight 与峰值恰为 3；
3. 6 个 worker × 4 轮 acquire/process/release，令牌不丢不重，GC 发生在持令牌期间；
4. 已取消 ctx 的 4 个等待者立即返回 `context.Canceled`，不吞令牌；取消与令牌同时可用时也归还令牌。

# 期望行为

1. 令牌总数始终等于容量 3，in-flight 结束时为 0；
2. holder 阶段峰值并发恰为 3，持令牌期间 GC 不破坏 payload；
3. 循环阶段 24 次服务全部完成，残留令牌数为 3，峰值不超过容量；
4. 预取消的等待者全部返回 Canceled，且未改变 in-flight；
5. 最后一行 `ALL PASS l02_chan_semaphore_tokens`。

# 失败判读

- `TryAcquire must fail on empty`：令牌泄漏或 select/default 分支错误；
- `held in-flight` / `peak`：信号量未限制并发；
- `tokens=N want=3`：Release 未归还或重复归还令牌；
- `cancel waiter ... want=context.Canceled`：取消路径未生效或吞掉令牌；
- `token payload corrupted`：令牌指针在 GC 后失效。

# 参数

- `UAF_SEED`：决定 token payload 派生值；
- 无 TORTURE 参数（固定容量 3 / 6 worker × 4 轮）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_chan_semaphore_tokens/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
