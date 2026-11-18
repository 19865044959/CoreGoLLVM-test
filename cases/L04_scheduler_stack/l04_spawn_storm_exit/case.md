---
id: l04_spawn_storm_exit
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: internal/leakcheck/leakcheck_test.go:41 goroutine 创建/退出计数与生命周期跟踪
  - pion/webrtc@v4.1.2: operations_test.go:13 100 轮 × 16 个 operation 的 goroutine 调度与汇合节奏
tags: [L04, goroutine, spawn-storm, immediate-exit, WaitGroup, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC leakcheck 与 WebRTC operations 中"大批短生命周期 goroutine 创建后立即退出"的路径。每轮一次性拉起 `TORTURE_SPAWN`（默认 512）个 goroutine，每个在自己的新栈上构造 3 节点堆指针链，固定 `Gosched` 8 次并复验链后立刻退出，不做任何跨 goroutine 阻塞协调。所有 goroutine 启动后、风暴仍在进行时主协程显式 GC；`WaitGroup` 汇合后再 GC 并逐项检查保存的链。

# 期望行为

1. 每个 goroutine 恰好完成一次，完成计数等于本轮启动数；
2. 风暴中与结束后，每个 goroutine 的 3 节点链 id/值关系 `b, b+1, b+2` 不变；
3. 全部 goroutine 的校验和总和等于独立公式 `8 × Σ(3b+3)`；
4. 保存到 `keep` 的指针在结束 GC 后仍指向完整链（clobberfree 下值不被破坏）；
5. 进程退出码为 0，最后一行输出 `ALL PASS l04_spawn_storm_exit`。

# 失败判读

- `done=... want=...`：goroutine 未全部执行，检查 WaitGroup/调度器；
- `chain corrupted`：新建 goroutine 栈上的指针或堆链在 GC 后损坏，优先怀疑栈复制/栈映射；
- `sum=... want=...`：有 goroutine 使用了错误的种子或链值；
- 超时：创建风暴后调度器无法推进。

# 参数

- `UAF_SEED`：链值种子（runner 每次重复注入）；
- `TORTURE_ROUNDS`：轮数，默认 4（1–16）；
- `TORTURE_SPAWN`：每轮 goroutine 数，默认 512（8–4096）。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_spawn_storm_exit/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
