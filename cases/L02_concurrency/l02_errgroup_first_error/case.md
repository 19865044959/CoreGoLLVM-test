---
id: l02_errgroup_first_error
dim: L02_concurrency
priority: P1
sources:
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:105 100 个并发 TrySchedule 回调必须全部执行，WaitGroup 收敛不丢唤醒
  - etcd@v3.5.0: raft/node_test.go:80 select 同时监听 done 与 context.Cancel 解除阻塞，停止后保持幂等
  - quic-go@v0.50.1: streams_map_outgoing_test.go:456 并发任务中取消部分 context，其余继续完成
tags: [L02, errgroup, first-error, context-cancel, WaitGroup, sync.Once, gc, UAF_SEED]
requires: []
runtime: "<3s"
---

# 场景

用标准库实现 errgroup 类模式：`group` 持有 `context.WithCancel` 派生 context，`Go` 启动子任务并用 `WaitGroup` 收敛，首个非 nil 错误通过 `sync.Once` 记录并 `cancel()`，`Wait` 返回该错误。

两个阶段：

1. 成功组：8 个任务并发执行，每个在 context 未取消时分配垃圾、显式 GC、校验 `*payload`，全部返回 nil；`Wait` 必须返回 nil，且 `Wait` 结束时派生 context 已被取消；
2. 失败组：6 个任务同时启动，任务 2 等主 goroutine 放行后返回哨兵错误 `boom`；其余任务阻塞在 `ctx.Done()`，被取消后返回 nil，其中任务 5 额外返回 `late error`。主 goroutine 用有界自旋等到 6 个任务全部启动后才放行任务 2。

# 期望行为

1. 成功组 8 个任务全部执行，`Wait` 返回 nil，派生 context 在 `Wait` 后为 `context.Canceled`；
2. 失败组 `Wait` 返回的错误 `errors.Is(err, errBoom)` 为真；
3. 晚到的 `late error` 不覆盖首个错误；
4. 失败组 5 个非失败任务都被取消唤醒并完成清理，`finishedOK==5`；
5. 失败组派生 context 为 `context.Canceled`；
6. 所有任务 payload 在多轮显式 GC 后保持内容一致；
7. 最后一行输出 `ALL PASS l02_errgroup_first_error`，退出码 0。

# 失败判读

- `not all group tasks started`：任务启动同步失败或 goroutine 未调度；
- `Wait error=... want boom`：首个错误记录/取消广播错误，或晚到错误覆盖了首个错误；
- `canceled-ok tasks=N want=5`：context 取消未传播到所有子任务；
- `payload corrupted after GC`：任务闭包持有的指针在 GC 后失效；
- 超时/死锁：`waitFor` 达到上限，说明启动计数器或 ctx 唤醒链断裂。

# 参数

- `UAF_SEED`：决定 payload 数值；
- 无 `TORTURE_*`；迭代固定，无墙钟等待。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_errgroup_first_error/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
GOGC=1 GOMAXPROCS=1 GODEBUG=clobberfree=1,asyncpreemptoff=1 /tmp/case
```
