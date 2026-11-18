---
id: l02_work_stealing_deque
dim: L02_concurrency
priority: P2
sources:
  - go@1.24.1: src/runtime/proc.go:6986 runqsteal 从其它 P 的 run queue 窃取；:6865 runqget 所有者取队列
  - etcd@v3.5.0: pkg/schedule/schedule_test.go:22 FIFO 调度器串行执行 100 个 job，Stop 收尾
  - nats-server@v2.11.0: server/ipqueue_test.go:253 三生产者并发 push、单消费者批量 pop
tags: [L02, work-stealing, deque, LIFO-owner, FIFO-thief, mutex, checksum, gc, UAF_SEED, TORTURE_ROUNDS]
requires: []
runtime: "<3s（默认 4 worker × 96 任务；TORTURE_ROUNDS 放大任务数，上限 8 倍）"
---

# 场景

模拟 Go runtime 的 work-stealing 调度：4 个 worker 各持一个私有 deque，所有者从队尾取任务（LIFO，保持热数据），空闲者从队头窃取（FIFO，保证公平）。任务不均衡分配（worker 0 约 3/4），所有 worker 启动后先强制窃取一次，确保窃取路径被覆盖。每个任务只允许执行一次，用 per-task 计数与校验和验证完整性。

先在单线程下精确验证 deque 语义：push 1/2/3 后 owner pop 得到 3、thief steal 得到 1、owner pop 得到 2、空 deque steal 返回 nil。

# 期望行为

1. 单线程 deque 的 owner-LIFO / thief-FIFO 顺序正确；
2. 并发阶段全部任务恰好执行一次，per-task `seen == 1`；
3. 校验和等于 `sum(seed + id*13)`，窃取次数不少于 worker 数；
4. 结束后所有 deque 为空，任务 payload 经 GC 仍完整；
5. 最后一行 `ALL PASS l02_work_stealing_deque`。

# 失败判读

- `owner pop must return newest` / `thief steal must return oldest`：deque 两端语义颠倒；
- `task N executed M times` / `seen`：窃取与所有者并发取到同一任务（缺少互斥或索引错误）；
- `checksum`：任务丢失、重复或 payload 被破坏；
- `deque not drained`：worker 在仍有任务时提前退出；
- 超时：终止条件 `executed == total` 的唤醒丢失。

# 参数

- `UAF_SEED`：决定任务 payload 派生值；
- `TORTURE_ROUNDS`：放大总任务数（默认 96，最大 8 倍）。

# 运行

```bash
go build -o /tmp/case ./cases/L02_concurrency/l02_work_stealing_deque/*.go
UAF_SEED=1 GODEBUG=clobberfree=1 /tmp/case
TORTURE_ROUNDS=4 UAF_SEED=2 /tmp/case
```
