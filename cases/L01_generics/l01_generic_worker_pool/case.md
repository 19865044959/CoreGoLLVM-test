---
id: l01_generic_worker_pool
dim: L01_generics
priority: P2
sources:
  - kitex@v0.13.0: client/rpctimeout_pool_test.go:32 100 个任务并发 RunTask，全部执行且池最终收缩
  - go-redis@v9.8.0: bench_decode_test.go:275 固定 worker 数与动态 goroutine 两种模式的压测
  - pgx@v5.7.4: pgxpool/pool_test.go:904 100 goroutine 并发借还，池上限与唤醒统计
tags: [L01, generics, worker-pool, goroutine, channel, WaitGroup, deterministic-results, gc, TORTURE_ROUNDS, UAF_SEED]
requires: [generics]
runtime: "<10s（默认 2 轮；TORTURE_ROUNDS 放大轮数，上限 8）"
---

# 场景

自建泛型 worker pool `Pool[T any]`：固定 worker 从带缓冲 channel 取 `job[T]`，按 job id 把结果写入预分配结果数组；提供幂等 `Close`（关闭 job channel）与关闭后 `Submit` 拒绝语义。对应服务框架里固定 worker 消费任务、以及连接池/任务池在关闭后拒绝新请求的路径。

主 goroutine 连续提交带 blob 的 `*rec` 任务，提交中途与全部提交后分别显式 GC；随后 Close、验证重复 Close 与关闭后 Submit 返回 false，再 Wait 并逐个校验结果值与负载指针/内容。每轮 worker 数、任务数由 UAF_SEED 决定。最后实例化 `Pool[int64]`，验证值型负载的独立实例化。

# 期望行为

1. 进程退出码为 0，最后一行输出 `ALL PASS l01_generic_worker_pool`；
2. 每个提交的任务恰好执行一次，`results[i] == f(orig[i])`，无遗漏、无串写；
3. 所有任务负载指针与 blob 在 GC 后保持不变；
4. Close 幂等，Close 后 Submit 立即返回 false 且不 panic；
5. `Wait` 后所有 worker 退出，无死锁；不同 rounds 之间状态隔离；
6. TORTURE_ROUNDS 放大后输出仍确定，clobberfree 等 profile 下一致。

# 失败判读

- `result ... corrupted`：job id 串写、结果数组竞态或任务丢失；
- `submit after close was accepted`：关闭状态机错误（closed 标志与 channel 关闭次序）；
- 超时/挂死：worker 未消费或 Close 后仍有阻塞发送，检查 channel 容量与 Wait/Close 顺序；
- bad pointer：任务负载在跨 goroutine 后丢失，怀疑栈映射或写屏障。

## 参数

- `UAF_SEED`：决定每轮任务数与 worker 数；
- `TORTURE_ROUNDS`：默认 2，最大 8，放大整体轮数。

## 运行

```bash
go build -o /tmp/case ./cases/L01_generics/l01_generic_worker_pool/main.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=5 TORTURE_ROUNDS=8 GODEBUG=clobberfree=1 /tmp/case
```
