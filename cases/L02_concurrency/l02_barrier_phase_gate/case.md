---
id: l02_barrier_phase_gate
dim: L02_concurrency
priority: P1
sources:
  - tailscale@v1.80.2: syncs/syncs_test.go:167 并发 LoadOrStore 用 start barrier 提高碰撞概率
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:105 100 个并发 TrySchedule 必须全部执行且不丢唤醒
  - etcd@v3.5.0: pkg/wait/wait_time_test.go:54 10k 个等待者单次广播全部唤醒
tags: [L02, barrier, phase-gate, generation, break, broadcast, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

模拟 tailscale 并发测试的 start barrier 与 etcd 的一代广播语义：6 个 worker 跑 4 个阶段，每个阶段先完成本阶段工作并累加 `done[p]`，再 `Wait`。barrier 只在全部 6 个 worker 到达后释放当前代，因此任意 worker 从 `Wait` 返回时必须看到 `done[p] == 6`。另起一个 2 人 barrier 验证 `Break` 让全部等待者返回失败，并验证 break 后同一 barrier 可重新用于下一代。

所有阶段推进只靠 channel 关闭与计数，不依赖墙钟；worker payload 在阶段 1 的 GC 前后校验。

# 期望行为

1. 每个阶段释放时 `done[p] == 6`、`passed[p] == 6`，没有 worker 提前进入下一阶段；
2. 正常释放时 6 个等待者全部返回成功；
3. `Break` 时已到达的 2 个等待者全部返回失败，且不阻塞；
4. break 后 barrier 可复用，下一代 4 个等待者全部返回成功；
5. payload 在 clobberfree/GC 后保持不变；
6. 最后一行 `ALL PASS l02_barrier_phase_gate`。

# 失败判读

- `phase N advanced early`：barrier 在未集齐 worker 时提前释放，代计数/唤醒逻辑错误；
- `barrier broken unexpectedly`：正常代被误标记为 broken；
- `waiters released by break must report failure`：Break 未传播失败状态；
- `barrier not reusable after break`：break 后 generation/brokenGen 未正确推进；
- 超时：等待者未被任何一代唤醒。

# 参数

- `UAF_SEED`：决定 worker payload 的派生值，失败输出可对照该种子；
- 无 TORTURE 参数（固定 6 worker × 4 阶段）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_barrier_phase_gate/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
