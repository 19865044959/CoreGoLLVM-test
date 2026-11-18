---
id: l02_semaphore_weighted_ctx
dim: L02_concurrency
priority: P1
sources:
  - tailscale@v1.80.2: syncs/syncs_test.go:145 容量 2 的 Semaphore：Acquire/TryAcquire/Release，已取消 ctx 的 Acquire 立即失败
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 100 个并发打开流中取消部分 context，等待者必须从等待集合移除
  - etcd@v3.5.0: raft/node_test.go:80 select 同时监听 done、ctx.Done 与目标 channel 解除阻塞
tags: [L02, semaphore, weighted, fifo-waiters, context-cancel, TryAcquire, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 tailscale `syncs.Semaphore` 的容量/取消/释放语义，使用自实现的加权 FIFO 信号量：容量 3，主 goroutine 先占满全部令牌，再提交 9 个普通等待者与 4 个带可取消 context 的等待者。

主 goroutine 用有界自旋 + `runtime.Gosched` 等到 waiter 队列恰为 13，取消 4 个 context，确认它们从等待队列移除且从未获得令牌；随后逐一释放 3 个令牌，FIFO 唤醒 9 个普通 worker。每个 worker 持令牌期间分配垃圾并 `runtime.GC()`，跨 GC 校验 `*token` payload。

另验证 `TryAcquire` 的容量边界、超限失败、`Release` 归还容量、预先取消的 `Acquire` 不消耗容量也不残留 waiter。

# 期望行为

1. `TryAcquire(2)` 在空信号量上成功，第二次 `TryAcquire(2)` 因超出容量失败，`Release(2)` 后 `cur==0`；
2. 已取消 context 的 `Acquire` 返回 `context.Canceled`，不消耗容量、不残留 waiter；
3. 13 个等待者在容量被主 goroutine 占满时全部登记为 waiter；
4. 取消 4 个 context 后 waiter 数回到 9，取消的 worker 一个都没有获得令牌；
5. 释放 3 个令牌后 9 个普通 worker 全部完成，峰值并发恰为 3，最终 `cur==0`、waiter 队列为空；
6. 所有 `*token` 在多次显式 GC 后内容不变；
7. 最后一行输出 `ALL PASS l02_semaphore_weighted_ctx`，退出码 0。

# 失败判读

- `not all workers enqueued as waiters` / `canceled waiters were not removed`：等待队列登记或取消移除逻辑错误，可能死锁；
- `canceled worker unexpectedly acquired a token`：取消与授予竞态下容量记账错误；
- `semaphore leaked capacity`：`Release`/取消路径未归还容量；
- `peak=N want=3`：同时持令牌数超过容量，互斥或唤醒条件错误；
- `payload corrupted after GC`：令牌指针在 GC 后失效，检查栈/堆指针位图。

# 参数

- `UAF_SEED`：决定 token payload 数值，失败输出中的期望值随之变化；
- 无墙钟阈值；同步全部依赖 waiter 计数与 channel 协调。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_semaphore_weighted_ctx/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1,asyncpreemptoff=1 /tmp/case
```
