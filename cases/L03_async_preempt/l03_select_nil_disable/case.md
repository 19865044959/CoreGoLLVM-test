---
id: l03_select_nil_disable
dim: L03_async_preempt
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:141 Close 后已入队回调仍须执行，select 区分 ctx.Done 与队列信号
  - etcd@v3.5.0: raft/node_test.go:80 Cancel/Stop 必须解除 Step 阻塞，select 同时监听 done 与目标 channel
tags: [L03, async-preempt, select, nil-channel, unbuffered-channel, deterministic-phases, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

单生产者按 4 个阶段向两个无缓冲 channel 发送节点：A 半批、B 半批、A 剩余、B 剩余。worker 的 select 只保留当前阶段的 channel，其余置 nil，等另一 channel 达到阈值后再恢复；worker 每收 8 条显式 GC，另有 GC pump 并发。判定只依赖计数与和，不依赖 select 的随机公平性。

# 期望行为

1. 两个 channel 各收到 32 个节点，无丢失、无重复；
2. nil-channel 禁用/恢复期间 worker 不读取错误节点；
3. 总和等于两组节点值之和；
4. worker 显式 GC 次数为 8；
5. GC pump 至少完成一轮；
6. 两组节点在 GC 后保持不变；
7. 退出码 0，最后一行 `ALL PASS l03_select_nil_disable`。

# 失败判读

- `FAIL: select loop received all values`：nil-channel 恢复逻辑错误或 select 丢唤醒；
- `FAIL: nil-disabled select delivered only intact nodes`：收到被回收/覆盖的节点；
- `FAIL: select loop checksum`：节点值或接收阶段错误；
- 超时：生产者与 worker 的阶段信号死锁。

# 参数

- `UAF_SEED`：决定 A/B 两组节点的发送旋转偏移，失败信息带出种子。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_select_nil_disable/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
