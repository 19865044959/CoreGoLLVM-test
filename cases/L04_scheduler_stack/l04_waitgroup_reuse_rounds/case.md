---
id: l04_waitgroup_reuse_rounds
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:105 100 个并发回调各自 wg.Done，全部执行且不丢唤醒
  - pion/webrtc@v4.1.2: operations_test.go:55 GracefulClose 前的计数保持不变，用 WaitGroup 验证关闭前任务
tags: [L04, WaitGroup, reuse, Add-Wait, goroutine, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC callback serializer 与 WebRTC operations 中反复使用 WaitGroup 汇合 goroutine：同一个 `sync.WaitGroup` 跨 6 轮 × 2 阶段（共 12 次 Add/Wait）反复复用。每阶段 32 个 worker 在各自栈上构造 2 节点链，发出就绪后停在 gate 上；主协程在计数非零、链仍在栈上时 GC，再放行并 `Wait`。每阶段记录运行次数与校验和，验证复用计数已正确归零、旧阶段结果不会串入新阶段；阶段间再 GC。

# 期望行为

1. 每个阶段每个 worker 恰好运行一次，无提前 Wait、无跨阶段串号；
2. 每阶段校验和等于 `2b+1`；
3. 每阶段的链在阶段内 GC 后仍为 `b, b+1`；
4. 全部 384 条 kept 链在最终 GC 后仍完整，数量等于 `6×2×32`；
5. 进程退出码为 0，最后一行输出 `ALL PASS l04_waitgroup_reuse_rounds`。

# 失败判读

- `ran ... times`：WaitGroup 复用后计数未归零，Wait 提前返回；
- `sum=...`/`chain corrupted`：worker 栈上数据在 GC 后损坏；
- `kept=... want=...`：有阶段未执行或结果丢失；
- 超时：gate 未关闭或 WaitGroup 计数泄漏。

# 参数

- `UAF_SEED`：链值种子；
- 固定 6 轮 × 2 阶段 × 32 worker。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_waitgroup_reuse_rounds/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
