---
id: l04_preempt_tight_loop
dim: L04_scheduler_stack
priority: P2
sources:
  - tailscale@v1.80.2: syncs/shardvalue_test.go:16 1000 goroutine × 10000 次原子计数长循环与调度点
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37 100 goroutine 并发调度与单消费者串行执行
tags: [L04, preemption, tight-loop, starvation, GOMAXPROCS, async-preempt, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<10s（默认轮数，TORTURE_ROUNDS/TORTURE_ITERS 可放大）"
---

# 场景

模拟 shardvalue/callback serializer 中"大量 goroutine 同时做固定量 CPU 工作"的调度压力，并观察紧循环下的饥饿：GOMAXPROCS 固定为 1，4 个 worker 先在栈上构造 3 节点链并停在 gate 上，主协程 GC 后放行。每个 worker 执行 `TORTURE_ITERS` 次纯算术紧循环（循环体内无任何显式让出），只依赖运行时异步抢占；独立 observer 以固定轮询预算（`1<<23` 次，每次 `Gosched`）等待完成计数达到 worker 数，预算耗尽即判定调度饥饿。

# 期望行为

1. 每个 worker 的紧循环结果等于独立计算的 `tightAcc(id, iters)`；
2. 全部 worker 在 observer 的固定轮询预算内完成，未判定饥饿；
3. observer 观测到的完成计数单调且始终落在 `[0, workers]`；
4. gate 释放前的 GC 后，worker 栈上的 3 节点链仍为 `b, b+1, b+2`；
5. 全部轮次后再次 GC，保存的链仍完整，GOMAXPROCS 恢复旧值；
6. 进程退出码为 0，最后一行输出 `ALL PASS l04_preempt_tight_loop`。

# 失败判读

- `observer exhausted poll budget`：紧循环长期不让出，异步抢占/调度器异常；
- `progress counter observed out of order`：完成计数出现撕裂或回退；
- `checksum mismatch`：紧循环计算被错误优化或栈数据损坏；
- `chain corrupted`：紧循环期间 GC 的栈扫描/栈映射错误；
- 超时：GOMAXPROCS=1 下活锁或抢占完全失效。

# 参数

- `UAF_SEED`：链值种子；
- `TORTURE_ROUNDS`：轮数，默认 2（1–12）；
- `TORTURE_ITERS`：每 worker 紧循环迭代数，默认 2000000（100000–20000000）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_preempt_tight_loop/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4 TORTURE_ITERS=4000000 /tmp/case
```
