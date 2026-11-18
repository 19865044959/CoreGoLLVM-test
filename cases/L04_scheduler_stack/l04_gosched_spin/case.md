---
id: l04_gosched_spin
dim: L04_scheduler_stack
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:105 100 个并发 TrySchedule 全部执行且不丢唤醒
  - tailscale@v1.80.2: types/lazy/sync_test.go:127 10000 goroutine 同时首次 Get，初始化只执行一次且全部得到结果
tags: [L04, Gosched, spin, preemption, GPM, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 gRPC callback serializer 与 tailscale lazy sync 中“大量 goroutine 在热点上自旋等待共享状态”的路径：默认 8 个 worker 各持 3 节点私有链，发出 ready 后进入 `for { if epoch!=0 break; runtime.Gosched(); spins++ }` 自旋。主协程收齐 ready 后，在自旋者持有栈上指针链且不断让出 CPU 时显式 GC 两次，再置位 `epoch`。每个 worker 退出自旋后遍历链，校验长度与校验和；若自旋次数达到固定上限 `1<<24` 仍未观察到 epoch，则判为饥饿/活性失败。

# 期望行为

1. 所有 worker 都能在固定自旋上限内观察到 epoch 退出，不依赖墙钟；
2. 自旋期间发生的 GC 不破坏栈上的 3 节点链；
3. 每个 worker 的链校验和等于 `3*base+21`；
4. 全部 worker 各返回一次结果，无缺失；
5. 总自旋次数大于 0，证明自旋循环确实执行；
6. 最后一行输出 `ALL PASS l04_gosched_spin`。

# 失败判读

- `starved or chain corrupted`：worker 在自旋上限内未看到 epoch，或 Gosched/GC 后链被破坏；
- `workers never executed the spin loop`：worker 在置位前就退出，自旋逻辑未生效；
- 超时：Gosched 未让出 CPU 导致主协程无法运行（调度器/抢占问题）；
- 仅 single 或 k8s profile 下失败：GOMAXPROCS=1/2 时的公平性与抢占问题。

# 参数

- `UAF_SEED`：链基值种子（默认 1）；
- `TORTURE_WORKERS`：自旋 worker 数，默认 8，范围 2–32；
- 固定自旋上限 `1<<24` 为活性检查，不作为耗时阈值。

# 运行

```bash
go build -o /tmp/case ./cases/L04_scheduler_stack/l04_gosched_spin/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=2 TORTURE_WORKERS=16 GOGC=1 GOMAXPROCS=2 /tmp/case
```
