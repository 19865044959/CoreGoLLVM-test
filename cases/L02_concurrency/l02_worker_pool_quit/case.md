---
id: l02_worker_pool_quit
dim: L02_concurrency
priority: P0
sources:
  - fasthttp@v1.59.0: workerpool_test.go:18 有界 worker pool 10 次 Start/Stop
  - fasthttp@v1.59.0: workerpool_test.go:56 WorkerFunc 阻塞时并发数不超上限
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:105 100 个并发 TrySchedule 必须全部执行且不丢唤醒
tags: [L02, worker-pool, quit-channel, WaitGroup, peak-concurrency, barrier, gc]
requires: []
runtime: "<2s"
---

# 场景

模拟 fasthttp worker pool 的 Start/Stop 与 grpc-go 并发调度收敛：3 轮，每轮启动固定 4 个 worker 从无缓冲 `jobs` 取任务，`quit` channel 负责统一退出。

每轮先投递 4 个 barrier 任务：每个 worker 各持一个任务并阻塞在同一个 `release` channel 上，主 goroutine 收齐 4 个 `held` 信号后，确定性地断言 in-flight 与峰值为 4，并在释放前执行一次显式 GC。释放后继续投递剩余 44 个任务；全部处理完（processed==48）再 `close(quit)`，worker 只能从 select 的 quit 分支退出。

# 期望行为

1. barrier 阶段 in-flight 恰为 4、峰值恰为 4，任务指针跨 GC 后内容不变；
2. 每轮 48 个任务全部处理，退出后 in-flight 为 0、无残留任务、4 个 worker 全部退出；
3. 峰值始终不超过 worker 数；
4. 3 轮结束后全局峰值固定为 4，输出确定；
5. 最后一行 `ALL PASS l02_worker_pool_quit`。

# 失败判读

- `barrier inflight/peak`：worker 未真正并发持有任务，或池上限失控；
- `processed`：quit 提前生效导致任务丢失，或任务计数错误；
- `exited workers`：quit 未唤醒全部 worker（死锁会先表现为超时）；
- `job corrupted after GC`：任务指针在 GC 后失效。

# 参数

- `UAF_SEED`：决定 job tag 派生值，失败输出带种子；
- 无 TORTURE 参数（P0 固定 3 轮 × 48 任务）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_worker_pool_quit/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
```
