---
id: l03_select_worker_preempt
dim: L03_async_preempt
priority: P1
sources:
  - etcd@v3.5.0: raft/node_test.go:353 node.run 单 goroutine select{tick, prop, recv, ready, stop} 事件循环
  - grpc-go@v1.71.1: internal/grpcsync/callback_serializer_test.go:37 单消费者串行执行调度任务，校验执行序
tags: [L03, async-preempt, select, worker-loop, tight-loop, gc, UAF_SEED]
requires: []
runtime: "<10s"
---

# 场景

模拟 select 驱动的事件循环 worker：worker 从 `jobCh` 接收 1024 个任务，`select` 同时监听 stop；每个任务内部是 512 次无调用迭代（持任务指针），每处理 16 个任务执行一次 `runtime.GC()`。主 goroutine 并发发送任务，另起 GC pump 持续分配垃圾并 GC。任务发送顺序由 `UAF_SEED` 决定。

# 期望行为

1. worker 处理完全部任务并返回，累加和等于 `512 * sum(v)`；
2. worker 在循环内执行的 GC 次数恰为 `1024/16 = 64`；
3. 背景 GC pump 至少完成一轮；
4. GC 后 1024 个任务节点值不变；
5. 进程退出码为 0，最后一行 `ALL PASS l03_select_worker_preempt`；
6. 各 profile 下无死锁、结果一致。

# 失败判读

- `FAIL: worker checksum across select loop (seed=...)`：任务丢失、重复或内层累加被 GC 破坏；
- `FAIL: worker ran periodic GC inside loop`：循环内 GC 路径未被执行；
- `FAIL: concurrent GC pump ran`：背景 GC 未完成；
- `FAIL: job nodes intact after GC (seed=...)`：任务指针目标被回收；
- 超时：channel/select 与 GC 之间死锁。

# 参数

- `UAF_SEED`：任务发送排列种子，默认 1，失败输出带出；
- 任务数、内层次数、GC 周期均为固定常量。

# 运行

```bash
go build -o /tmp/case ./cases/L03_async_preempt/l03_select_worker_preempt/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
UAF_SEED=9 GOGC=1 GOMAXPROCS=2 GODEBUG=clobberfree=1 /tmp/case
```
